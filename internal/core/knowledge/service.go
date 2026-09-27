package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

func (s *Service) read(ctx context.Context, tx pgx.Tx, e Entity) (*Page, error) {
	p, err := s.snapshot(ctx, tx, e)
	if err != nil {
		return nil, err
	}
	var published, draft []byte
	err = tx.QueryRow(ctx, `SELECT content,source_hash,sources,mode,published_at,draft_content,draft_hash,draft_source_hash,draft_sources,draft_mode,draft_compiler_hash,compiled_at FROM knowledge_pages WHERE entity_type=$1 AND entity_id=$2`, e.Kind, e.ID).Scan(&p.Content, &p.SourceHash, &published, &p.Mode, &p.PublishedAt, &p.DraftContent, &p.DraftHash, &p.DraftSourceHash, &draft, &p.DraftMode, &p.DraftCompilerHash, &p.CompiledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read knowledge page: %w", err)
	}
	if err = json.Unmarshal(published, &p.PublishedSources); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(draft, &p.DraftSources); err != nil {
		return nil, err
	}
	if p.PublishedAt != nil {
		p.Status = "published"
		p.Freshness = "fresh"
		if p.SourceHash != p.CurrentSourceHash {
			p.Freshness = "stale"
			p.Content = ""
			p.PublishedSources = []Source{}
		}
	}
	if p.DraftHash != "" {
		if p.PublishedAt == nil {
			p.Status = "draft"
		}
		p.DraftFreshness = "fresh"
		if p.DraftSourceHash != p.CurrentSourceHash {
			p.DraftFreshness = "stale"
			p.DraftContent = ""
			p.DraftSources = []Source{}
		}
	}
	return p, nil
}

func (s *Service) Get(ctx context.Context, e Entity) (*Page, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	p, err := s.read(ctx, tx, e)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// lockSources makes the publication check and write atomic against native catalog writers.
// ponytail: brief table locks serialize source writes during publication; use source revisions if contention warrants it.
func lockSources(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `LOCK TABLE assets,data_products,glossary_terms,domains,doc_pages,documentation,global_documentation,memories,data_product_memberships,asset_terms,lineage_edges,asset_domains,data_product_domains,glossary_term_domains IN SHARE MODE`)
	return err
}

func (s *Service) Compile(ctx context.Context, e Entity) (*Page, error) {
	p, err := s.Get(ctx, e)
	if err != nil {
		return nil, err
	}
	mode := "extractive"
	if s.opts.Endpoint != "" {
		mode = "llm"
	}
	if p.DraftFreshness == "fresh" && p.DraftCompilerHash == s.compilerHash() {
		return p, nil
	}
	content, err := s.generate(ctx, p)
	if err != nil {
		return nil, err
	}
	digest := hash([]string{p.CurrentSourceHash, content, s.compilerHash()})
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockSources(ctx, tx); err != nil {
		return nil, err
	}
	current, err := s.snapshot(ctx, tx, e)
	if err != nil {
		return nil, err
	}
	if current.CurrentSourceHash != p.CurrentSourceHash {
		return nil, ErrConflict
	}
	sources, err := json.Marshal(p.Sources)
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec(ctx, `INSERT INTO knowledge_pages(entity_type,entity_id,draft_content,draft_hash,draft_source_hash,draft_sources,draft_mode,draft_compiler_hash,compiled_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now()) ON CONFLICT(entity_type,entity_id) DO UPDATE SET draft_content=EXCLUDED.draft_content,draft_hash=EXCLUDED.draft_hash,draft_source_hash=EXCLUDED.draft_source_hash,draft_sources=EXCLUDED.draft_sources,draft_mode=EXCLUDED.draft_mode,draft_compiler_hash=EXCLUDED.draft_compiler_hash,compiled_at=now() WHERE knowledge_pages.draft_hash=$9`, e.Kind, e.ID, content, digest, p.CurrentSourceHash, sources, mode, s.compilerHash(), p.DraftHash)
	if err != nil {
		return nil, fmt.Errorf("store knowledge draft: %w", err)
	}
	if result.RowsAffected() != 1 {
		return nil, ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, e)
}

func (s *Service) Publish(ctx context.Context, e Entity, draftHash, actor string) (*Page, error) {
	if !s.valid(e) || draftHash == "" || actor == "" || len(actor) > 255 {
		return nil, ErrInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockSources(ctx, tx); err != nil {
		return nil, err
	}
	p, err := s.snapshot(ctx, tx, e)
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec(ctx, `UPDATE knowledge_pages SET content=draft_content,source_hash=draft_source_hash,sources=draft_sources,mode=draft_mode,published_at=now(),published_by=$5 WHERE entity_type=$1 AND entity_id=$2 AND draft_hash=$3 AND draft_source_hash=$4 AND draft_content<>''`, e.Kind, e.ID, draftHash, p.CurrentSourceHash, actor)
	if err != nil {
		return nil, fmt.Errorf("publish knowledge: %w", err)
	}
	if result.RowsAffected() != 1 {
		return nil, ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, e)
}

const catalogSQL = `WITH catalog AS (
 SELECT 'asset'::text kind,id::text,name title FROM assets WHERE NOT is_stub
 UNION ALL SELECT 'data_product',id::text,name FROM data_products
 UNION ALL SELECT 'glossary_term',id::text,name FROM glossary_terms WHERE deleted_at IS NULL
 UNION ALL SELECT 'domain',id::text,name FROM domains WHERE $1
)`

func bounds(q string, limit, offset int) (int, error) {
	if len(q) > 500 || offset < 0 || offset > 1000000 {
		return 0, ErrInvalid
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 {
		return 0, ErrInvalid
	}
	return limit, nil
}

func (s *Service) List(ctx context.Context, q string, limit, offset int) (*ListResult, error) {
	limit, err := bounds(q, limit, offset)
	if err != nil {
		return nil, err
	}
	result := &ListResult{Pages: []Page{}, Limit: limit, Offset: offset}
	query := catalogSQL + ` SELECT c.kind,c.id,c.title,count(*) OVER() FROM catalog c WHERE $2='' OR position(lower($2) in lower(c.title))>0 ORDER BY lower(c.title),c.kind,c.id LIMIT $3 OFFSET $4`
	rows, err := s.db.Query(ctx, query, s.opts.DomainsEnabled, strings.TrimSpace(q), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list knowledge: %w", err)
	}
	entities := []Page{}
	for rows.Next() {
		var e Page
		if err = rows.Scan(&e.Kind, &e.ID, &e.Title, &result.Total); err != nil {
			rows.Close()
			return nil, err
		}
		entities = append(entities, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(entities) == 0 {
		err = s.db.QueryRow(ctx, catalogSQL+` SELECT count(*) FROM catalog c WHERE $2='' OR position(lower($2) in lower(c.title))>0`, s.opts.DomainsEnabled, strings.TrimSpace(q)).Scan(&result.Total)
		if err != nil {
			return nil, err
		}
	}
	for _, e := range entities {
		p, err := s.Get(ctx, e.Entity)
		if errors.Is(err, ErrTooLarge) {
			e.EntityURL = entityURL(e.Entity)
			e.Status = "uncompiled"
			e.Freshness = "unverified"
			e.Error = ErrTooLarge.Error()
			result.Pages = append(result.Pages, e)
			continue
		}
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// The index carries summaries; full source lists and content are loaded on navigation.
		p.Content = ""
		p.DraftContent = ""
		p.Sources = []Source{}
		p.PublishedSources = []Source{}
		p.DraftSources = []Source{}
		result.Pages = append(result.Pages, *p)
	}
	return result, nil
}

func (s *Service) Context(ctx context.Context, q string, limit int) ([]Page, error) {
	limit, err := bounds(q, limit, 0)
	if err != nil {
		return nil, err
	}
	terms := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	terms = slices.DeleteFunc(terms, func(term string) bool { return len([]rune(term)) < 3 })
	if len(terms) > 12 {
		terms = terms[:12]
	}
	if strings.TrimSpace(q) != "" && len(terms) == 0 {
		return []Page{}, nil
	}
	// ponytail: lexical scans suffice for the current catalog size; add an indexed search when this becomes slow.
	rows, err := s.db.Query(ctx, catalogSQL+` SELECT c.kind,c.id FROM catalog c JOIN knowledge_pages k ON k.entity_type=c.kind AND k.entity_id=c.id
 CROSS JOIN LATERAL (SELECT coalesce(sum(CASE WHEN position(term in lower(c.title))>0 THEN 3 WHEN position(term in lower(k.content))>0 THEN 1 ELSE 0 END),0) relevance FROM unnest($2::text[]) term) score
 WHERE k.published_at IS NOT NULL AND (cardinality($2::text[])=0 OR score.relevance>0)
 ORDER BY score.relevance DESC,lower(c.title),c.kind,c.id LIMIT 100`, s.opts.DomainsEnabled, terms)
	if err != nil {
		return nil, err
	}
	entities := []Entity{}
	for rows.Next() {
		var e Entity
		if err = rows.Scan(&e.Kind, &e.ID); err != nil {
			rows.Close()
			return nil, err
		}
		entities = append(entities, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []Page{}
	for _, e := range entities {
		p, err := s.Get(ctx, e)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if p.Freshness != "fresh" || p.PublishedAt == nil {
			continue
		}
		p.DraftContent = ""
		p.DraftHash = ""
		p.DraftSourceHash = ""
		p.DraftSources = []Source{}
		p.DraftFreshness = ""
		p.DraftMode = ""
		p.CompiledAt = nil
		p.Sources = p.PublishedSources
		result = append(result, *p)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (s *Service) CompileBatch(ctx context.Context, limit, offset int) (*BatchResult, error) {
	list, err := s.List(ctx, "", limit, offset)
	if err != nil {
		return nil, err
	}
	result := &BatchResult{Errors: []BatchError{}, Total: list.Total, NextOffset: offset + len(list.Pages)}
	for _, p := range list.Pages {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		next, err := s.Compile(ctx, p.Entity)
		if err != nil {
			result.Errors = append(result.Errors, BatchError{Entity: p.Entity, Error: err.Error()})
			continue
		}
		if next.DraftHash == p.DraftHash {
			result.Skipped++
		} else {
			result.Compiled++
		}
	}
	if result.NextOffset >= result.Total {
		result.NextOffset = 0
	}
	return result, nil
}
