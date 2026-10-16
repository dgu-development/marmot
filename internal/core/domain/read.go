package domain

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/marmotdata/marmot/internal/core/auth"
)

// readCacheTTL bounds how long a change of roles or of a restricted mark may
// take to reach the reads of another replica. Every store read asks what is
// hidden, so resolving it each time would double the queries of a request.
const readCacheTTL = 2 * time.Second

const readCacheSize = 4096

type readView struct {
	at time.Time
	// content are the domains whose entities the caller may not read; nodes
	// are those it may not even see in the tree.
	content, nodes []string
}

type readCache struct {
	mu    sync.Mutex
	views map[string]readView
}

func (c *readCache) get(key string) (readView, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.views[key]
	return v, ok && time.Since(v.at) < readCacheTTL
}

func (c *readCache) put(key string, v readView) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.views == nil || len(c.views) >= readCacheSize {
		c.views = map[string]readView{}
	}
	c.views[key] = v
}

// ForgetReads drops what is remembered of who reads what, after a change to
// the tree or to its roles made through this server.
func (g *Guard) ForgetReads() {
	g.reads.mu.Lock()
	g.reads.views = nil
	g.reads.mu.Unlock()
}

// Reads returns whether the scope reaches the domain at path: any role there
// or in an ancestor does.
func (sc *Scope) Reads(path string) bool {
	if sc == nil {
		return false
	}
	if sc.Global {
		return true
	}
	for _, g := range sc.Grants {
		if strings.HasPrefix(path, g.Path) {
			return true
		}
	}
	return false
}

// hide splits the restricted subtrees into what the scope may not read and,
// of that, what it may not see: a domain above one it reads stays in the tree
// so the readable one can be reached, without its content.
func hide(restricted []*Domain, scope *Scope) (content, nodes []string) {
	var readable []string
	for _, d := range restricted {
		if scope.Reads(d.Path) {
			readable = append(readable, d.Path)
		}
	}
	for _, d := range restricted {
		if scope.Reads(d.Path) {
			continue
		}
		content = append(content, d.ID)
		above := false
		for _, path := range readable {
			if strings.HasPrefix(path, d.Path) {
				above = true
				break
			}
		}
		if !above {
			nodes = append(nodes, d.ID)
		}
	}
	return content, nodes
}

func (g *Guard) readView(ctx context.Context) (readView, error) {
	p, ok := g.principal(ctx)
	if !ok || p == nil {
		return readView{}, nil
	}
	key := string(p.Type()) + ":" + p.ID()
	if v, ok := g.reads.get(key); ok {
		return v, nil
	}
	v, err := g.resolveReadView(ctx, p)
	if err != nil {
		return readView{}, err
	}
	v.at = time.Now()
	g.reads.put(key, v)
	return v, nil
}

func (g *Guard) resolveReadView(ctx context.Context, p auth.Principal) (readView, error) {
	restricted, err := g.repo.RestrictedSubtrees(ctx)
	if err != nil || len(restricted) == 0 {
		return readView{}, err
	}
	scope, err := g.svc.Scope(ctx, p)
	if err != nil {
		return readView{}, err
	}
	var v readView
	v.content, v.nodes = hide(restricted, scope)
	return v, nil
}

// Hidden returns the IDs of the domains whose content the caller of ctx may
// not read: those in a restricted subtree it holds no role over. Work without
// a principal is the server's own and reads everything.
func (g *Guard) Hidden(ctx context.Context) ([]string, error) {
	v, err := g.readView(ctx)
	return v.content, err
}

// readable hides from the callers of a Service the domains they may not see.
type readable struct {
	Service
	g *Guard
}

// GuardReads returns the service the API serves: restricted domains the
// caller holds no role in are missing from it.
func GuardReads(inner Service, g *Guard) Service {
	return &readable{Service: inner, g: g}
}

func (s *readable) unseen(ctx context.Context) (map[string]bool, error) {
	v, err := s.g.readView(ctx)
	if err != nil || len(v.nodes) == 0 {
		return nil, err
	}
	unseen := make(map[string]bool, len(v.nodes))
	for _, id := range v.nodes {
		unseen[id] = true
	}
	return unseen, nil
}

func (s *readable) keep(ctx context.Context, domains []*Domain, err error) ([]*Domain, error) {
	if err != nil {
		return nil, err
	}
	unseen, err := s.unseen(ctx)
	if err != nil || unseen == nil {
		return domains, err
	}
	kept := make([]*Domain, 0, len(domains))
	for _, d := range domains {
		if !unseen[d.ID] {
			kept = append(kept, d)
		}
	}
	return kept, nil
}

func (s *readable) seen(ctx context.Context, id string) error {
	unseen, err := s.unseen(ctx)
	if err != nil {
		return err
	}
	if unseen[strings.ToLower(id)] {
		return ErrNotFound
	}
	return nil
}

func (s *readable) Get(ctx context.Context, id string) (*Domain, error) {
	if err := s.seen(ctx, id); err != nil {
		return nil, err
	}
	return s.Service.Get(ctx, id)
}

func (s *readable) Children(ctx context.Context, parentID *string) ([]*Domain, error) {
	domains, err := s.Service.Children(ctx, parentID)
	return s.keep(ctx, domains, err)
}

func (s *readable) Subtree(ctx context.Context, id string) ([]*Domain, error) {
	if err := s.seen(ctx, id); err != nil {
		return nil, err
	}
	domains, err := s.Service.Subtree(ctx, id)
	return s.keep(ctx, domains, err)
}

func (s *readable) Resolve(ctx context.Context, ref string) ([]*Domain, error) {
	domains, err := s.Service.Resolve(ctx, ref)
	return s.keep(ctx, domains, err)
}

func (s *readable) Roles(ctx context.Context, domainID string) ([]RoleAssignment, error) {
	if err := s.seen(ctx, domainID); err != nil {
		return nil, err
	}
	return s.Service.Roles(ctx, domainID)
}

// DomainOf answers for an entity, so what matters is whether its content is
// readable, not whether the domain shows in the tree.
func (s *readable) DomainOf(ctx context.Context, kind Kind, entityID string) (string, error) {
	id, err := s.Service.DomainOf(ctx, kind, entityID)
	if err != nil {
		return "", err
	}
	hidden, err := s.g.Hidden(ctx)
	if err != nil {
		return "", err
	}
	for _, h := range hidden {
		if h == id {
			return "", ErrNotFound
		}
	}
	return id, nil
}

// SubjectReadsAsset reports whether a user or a team may be told about the
// asset with that MRN. Unlike a caller, a subject is known only by its roles:
// an administrator without one in a restricted domain is not told either.
func (g *Guard) SubjectReadsAsset(ctx context.Context, subject SubjectType, subjectID, mrn string) (bool, error) {
	restricted, err := g.repo.RestrictedSubtrees(ctx)
	if err != nil || len(restricted) == 0 {
		return err == nil, err
	}
	ids, err := g.repo.AssetIDsByMRN(ctx, []string{mrn})
	if err != nil {
		return false, err
	}
	id, ok := ids[mrn]
	if !ok {
		return true, nil
	}
	domainID, err := g.repo.DomainOf(ctx, KindAsset, id)
	if err != nil {
		return false, err
	}
	for _, d := range restricted {
		if d.ID != domainID {
			continue
		}
		grants, err := g.repo.Grants(ctx, subject, subjectID)
		if err != nil {
			return false, err
		}
		return (&Scope{Grants: grants}).Reads(d.Path), nil
	}
	return true, nil
}

// authorizeRead refuses a write to what the caller may not read, whether or
// not writes are scoped by domain: nobody edits blind.
func (g *Guard) authorizeRead(ctx context.Context, kind Kind, ids []string) error {
	hidden, err := g.Hidden(ctx)
	if err != nil || len(hidden) == 0 || len(ids) == 0 {
		return err
	}
	placed, err := g.repo.Placements(ctx, kind, ids)
	if err != nil {
		return err
	}
	for _, p := range placed {
		for _, h := range hidden {
			if p.DomainID == h {
				return ErrForbidden
			}
		}
	}
	return nil
}
