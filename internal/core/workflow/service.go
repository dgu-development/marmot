package workflow

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/auth"
	"github.com/marmotdata/marmot/internal/core/domain"
	"github.com/marmotdata/marmot/internal/core/notification"
	"github.com/marmotdata/marmot/internal/core/team"
	"github.com/marmotdata/marmot/internal/core/user"
	"github.com/rs/zerolog/log"
)

// Notification types the engine emits.
const (
	TypeTaskAssigned     = "task_assigned"
	TypeTaskEscalated    = "task_escalated"
	TypeWorkflowDecision = "workflow_decision"
	TypeWorkflowMessage  = "workflow_message"
)

const (
	// TargetAsset is the only target kind in this version: the actions write
	// assets.
	TargetAsset = "asset"

	maxCommentLength = 2000
	timerBatch       = 50
	maxStartBatch    = 50
)

var (
	ErrForbidden = errors.New("forbidden")
	// ErrInvalidInput is a request the service refuses as such, not a
	// diagram problem (those are *ValidationError).
	ErrInvalidInput = errors.New("invalid input")
	// ErrNotRunnable is a definition that cannot start: not published.
	ErrNotRunnable = errors.New("definition is not published")
	// ErrNotRunning is an instance that already ended.
	ErrNotRunning = errors.New("instance is not running")

	decisionRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

type Users interface {
	Get(ctx context.Context, id string) (*user.User, error)
	GetUserByUsername(ctx context.Context, username string) (*user.User, error)
}

type Teams interface {
	GetTeam(ctx context.Context, id string) (*team.Team, error)
	GetTeamByName(ctx context.Context, name string) (*team.Team, error)
	ListMembers(ctx context.Context, teamID string) ([]*team.TeamMemberWithUser, error)
}

// Domains is nil when domains are disabled; role groups then name nobody.
type Domains interface {
	DomainOf(ctx context.Context, kind domain.Kind, entityID string) (string, error)
	Roles(ctx context.Context, domainID string) ([]domain.RoleAssignment, error)
}

type Assets interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
	PatchFields(ctx context.Context, id string, version int64, fields map[string]any) (*asset.Asset, error)
	AddTag(ctx context.Context, id string, tag string) (*asset.Asset, error)
	RemoveTag(ctx context.Context, id string, tag string) (*asset.Asset, error)
}

// QueryMatcher resolves a Discover/asset-rule query_expression to asset ids.
// Nil disables batch start by query.
type QueryMatcher interface {
	Match(ctx context.Context, queryExpression string, limit int) (ids []string, total int, err error)
}

type Notifier interface {
	Create(ctx context.Context, input notification.CreateNotificationInput) error
}

type Service struct {
	repo     *PostgresRepository
	users    Users
	teams    Teams
	domains  Domains
	assets   Assets
	queries  QueryMatcher
	notifier Notifier
	now      func() time.Time
}

func NewService(repo *PostgresRepository, users Users, teams Teams, domains Domains, assets Assets, notifier Notifier) *Service {
	return &Service{repo: repo, users: users, teams: teams, domains: domains, assets: assets, notifier: notifier, now: time.Now}
}

// WithQueries enables starting runs for every asset matching a query_expression.
func (s *Service) WithQueries(q QueryMatcher) *Service {
	s.queries = q
	return s
}

func principalUserID(p auth.Principal) (string, bool) {
	if p == nil || p.Type() != auth.PrincipalTypeUser {
		return "", false
	}
	return p.ID(), true
}

func canManage(p auth.Principal) bool {
	return p != nil && (p.IsAdmin() || p.HasPermission("workflows", "manage"))
}

// inspect reads what a draft needs even when it would not run: its process
// id and name. Only an unreadable document is refused.
func inspect(bpmn []byte) (key, name string, issues []Issue, err error) {
	p, parseErr := Parse(bpmn)
	if parseErr == nil {
		return p.ID, p.Name, []Issue{}, nil
	}
	var v *ValidationError
	if !errors.As(parseErr, &v) {
		return "", "", nil, parseErr
	}
	root, treeErr := parseTree(bpmn)
	if treeErr != nil || root.name.Space != NamespaceBPMN || len(root.bpmnChildren("process")) != 1 {
		return "", "", nil, parseErr
	}
	proc := root.bpmnChildren("process")[0]
	key = proc.attr("", "id")
	if key == "" {
		return "", "", nil, &ValidationError{Issues: []Issue{{Code: "missing_id", Detail: "process"}}}
	}
	return key, proc.attr("", "name"), v.Issues, nil
}

func withIssues(d *Definition) *Definition {
	if d == nil {
		return nil
	}
	d.Issues = []Issue{}
	if d.BPMN != "" {
		if _, _, issues, err := inspect([]byte(d.BPMN)); err == nil {
			d.Issues = issues
		}
	}
	return d
}

func displayName(key, name string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return key
}

// CreateDefinition stores a draft. A draft may still have issues; they come
// back with it and block publishing, not saving.
func (s *Service) CreateDefinition(ctx context.Context, p auth.Principal, bpmn []byte) (*Definition, error) {
	if !canManage(p) {
		return nil, ErrForbidden
	}
	key, name, _, err := inspect(bpmn)
	if err != nil {
		return nil, err
	}
	var by *string
	if id, ok := principalUserID(p); ok {
		by = &id
	}
	d, err := s.repo.CreateDefinition(ctx, key, displayName(key, name), string(bpmn), by)
	return withIssues(d), err
}

// UpdateDefinition replaces a draft. The process id must stay: it is what
// ties the versions of one workflow together.
func (s *Service) UpdateDefinition(ctx context.Context, p auth.Principal, id string, bpmn []byte) (*Definition, error) {
	if !canManage(p) {
		return nil, ErrForbidden
	}
	current, err := s.repo.GetDefinition(ctx, id)
	if err != nil {
		return nil, err
	}
	key, name, _, err := inspect(bpmn)
	if err != nil {
		return nil, err
	}
	if key != current.ProcessKey {
		return nil, fmt.Errorf("%w: the process id changed from %q to %q", ErrInvalidInput, current.ProcessKey, key)
	}
	d, err := s.repo.UpdateDraft(ctx, id, displayName(key, name), string(bpmn))
	return withIssues(d), err
}

func (s *Service) GetDefinition(ctx context.Context, id string) (*Definition, error) {
	d, err := s.repo.GetDefinition(ctx, id)
	return withIssues(d), err
}

// ListDefinitions lists every version to managers and only published ones to
// everybody else.
func (s *Service) ListDefinitions(ctx context.Context, p auth.Principal) ([]*Definition, error) {
	status := StatusPublished
	if canManage(p) {
		status = ""
	}
	return s.repo.ListDefinitions(ctx, status)
}

// Publish freezes a draft that runs.
func (s *Service) Publish(ctx context.Context, p auth.Principal, id string) (*Definition, error) {
	if !canManage(p) {
		return nil, ErrForbidden
	}
	d, err := s.repo.GetDefinition(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := Parse([]byte(d.BPMN)); err != nil {
		return nil, err
	}
	d, err = s.repo.SetDefinitionStatus(ctx, id, StatusDraft, StatusPublished)
	return withIssues(d), err
}

// Retire stops new runs of a published version; running ones go on.
func (s *Service) Retire(ctx context.Context, p auth.Principal, id string) (*Definition, error) {
	if !canManage(p) {
		return nil, ErrForbidden
	}
	d, err := s.repo.SetDefinitionStatus(ctx, id, StatusPublished, StatusRetired)
	return withIssues(d), err
}

func (s *Service) DeleteDraft(ctx context.Context, p auth.Principal, id string) error {
	if !canManage(p) {
		return ErrForbidden
	}
	return s.repo.DeleteDraft(ctx, id)
}

// Target is what an instance is about.
type Target struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// pending notifications are sent after the transaction commits, so a
// rolled-back step never notifies.
type pending []notification.CreateNotificationInput

func (s *Service) send(ctx context.Context, out pending) {
	if s.notifier == nil {
		return
	}
	for _, n := range out {
		if len(n.Recipients) == 0 {
			continue
		}
		if err := s.notifier.Create(context.WithoutCancel(ctx), n); err != nil {
			log.Warn().Err(err).Str("type", n.Type).Msg("Failed to queue workflow notification")
		}
	}
}

func users(ids []string) []notification.Recipient {
	out := make([]notification.Recipient, 0, len(ids))
	for _, id := range ids {
		out = append(out, notification.Recipient{Type: notification.RecipientTypeUser, ID: id})
	}
	return out
}

func instanceLink(id string) string { return "/dgu/workflows/runs/" + id }

const tasksLink = "/dgu/workflows/tasks"

// Start runs a published definition until its first waits.
func (s *Service) Start(ctx context.Context, p auth.Principal, definitionID string, target *Target) (*Instance, error) {
	initiator, ok := principalUserID(p)
	if !ok {
		return nil, ErrForbidden
	}
	def, err := s.repo.GetDefinition(ctx, definitionID)
	if err != nil {
		return nil, err
	}
	if def.Status != StatusPublished {
		return nil, ErrNotRunnable
	}
	proc, err := Parse([]byte(def.BPMN))
	if err != nil {
		return nil, err
	}
	vars := map[string]string{"initiator": initiator, "_participants": initiator}
	in := &Instance{DefinitionID: def.ID, DefinitionName: def.Name, Version: def.Version, Status: InstanceRunning, InitiatorID: &initiator}
	if target != nil && target.ID != "" {
		if target.Kind != TargetAsset {
			return nil, fmt.Errorf("%w: target kind must be %q", ErrInvalidInput, TargetAsset)
		}
		a, err := s.assets.Get(ctx, target.ID)
		if err != nil {
			if errors.Is(err, asset.ErrNotFound) {
				return nil, fmt.Errorf("%w: target asset not found", ErrInvalidInput)
			}
			return nil, err
		}
		name := ""
		if a.Name != nil {
			name = *a.Name
		}
		kind := TargetAsset
		in.TargetKind, in.TargetID, in.TargetName = &kind, &a.ID, &name
		vars["target_kind"], vars["target_id"], vars["target_name"] = kind, a.ID, name
	}
	in.State = NewState(vars)

	var out pending
	err = s.repo.WithTx(ctx, func(tx *PostgresRepository) error {
		if err := tx.InsertInstance(ctx, in); err != nil {
			return err
		}
		exec := &executor{svc: s, instance: in, principal: p}
		runner := &Runner{Process: proc, Executor: exec, Now: s.now}
		step := runner.Start(ctx, in.State)
		notes, err := s.apply(ctx, tx, in, step, &initiator)
		out = append(append(out, exec.notes...), notes...)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.send(ctx, out)
	return in, nil
}

// StartBatchResult is what StartQuery returns after starting one run per match.
type StartBatchResult struct {
	Instances []*Instance `json:"instances"`
	Total     int         `json:"total"`
	Started   int         `json:"started"`
	Limit     int         `json:"limit"`
}

// StartQuery starts one run per asset matching query_expression (same language
// as asset rules / Discover), capped at limit (default and max 50).
func (s *Service) StartQuery(ctx context.Context, p auth.Principal, definitionID, queryExpression string, limit int) (*StartBatchResult, error) {
	if s.queries == nil {
		return nil, fmt.Errorf("%w: query starts are not available", ErrInvalidInput)
	}
	q := strings.TrimSpace(queryExpression)
	if q == "" {
		return nil, fmt.Errorf("%w: query_expression is required", ErrInvalidInput)
	}
	if limit <= 0 || limit > maxStartBatch {
		limit = maxStartBatch
	}
	ids, total, err := s.queries.Match(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	out := &StartBatchResult{Total: total, Limit: limit, Instances: make([]*Instance, 0, len(ids))}
	for _, id := range ids {
		in, err := s.Start(ctx, p, definitionID, &Target{Kind: TargetAsset, ID: id})
		if err != nil {
			return out, err
		}
		out.Instances = append(out.Instances, in)
		out.Started++
	}
	return out, nil
}

// apply persists a step: tasks opened and closed, events, and the resulting
// status. It returns the notifications to send after commit.
func (s *Service) apply(ctx context.Context, tx *PostgresRepository, in *Instance, step *Step, actor *string) (pending, error) {
	var out pending
	for _, token := range step.CancelledTasks {
		if err := tx.CloseTask(ctx, in.ID, token, TaskCancelled, nil, nil, nil); err != nil && !errors.Is(err, ErrConflict) {
			return nil, err
		}
	}
	for _, nt := range step.NewTasks {
		candidates, err := s.candidates(ctx, nt.Node, in)
		if err != nil {
			return nil, err
		}
		t := &Task{InstanceID: in.ID, TokenID: nt.TokenID, NodeID: nt.Node.ID, Name: displayName(nt.Node.ID, nt.Node.Name), Candidates: candidates, DueAt: nt.DueAt}
		if nt.Timer != "" {
			timer := nt.Timer
			t.TimerNode = &timer
		}
		if err := tx.InsertTask(ctx, t); err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			step.Events = append(step.Events, StepEvent{Type: "task_unassigned", Element: nt.Node.ID})
		}
		out = append(out, notification.CreateNotificationInput{
			Recipients: users(candidates),
			Type:       TypeTaskAssigned,
			Title:      t.Name,
			Message:    fmt.Sprintf("%s: %s", in.DefinitionName, t.Name),
			Data:       s.data(in, map[string]any{"task_id": t.ID, "link": tasksLink}),
		})
	}
	switch {
	case step.Failure != nil:
		in.Status = InstanceFailed
		code, element := step.Failure.Code, step.Failure.Element
		in.FailureCode, in.FailureElement = &code, &element
		if step.Failure.Err != nil {
			detail := step.Failure.Err.Error()
			in.FailureDetail = &detail
		}
		if err := tx.CancelOpenTasks(ctx, in.ID); err != nil {
			return nil, err
		}
		step.Events = append(step.Events, StepEvent{Type: "failed", Element: element, Detail: map[string]any{"code": code}})
	case step.Done:
		in.Status = InstanceCompleted
		step.Events = append(step.Events, StepEvent{Type: "completed"})
	}
	if err := tx.InsertEvents(ctx, in.ID, actor, step.Events); err != nil {
		return nil, err
	}
	if err := tx.SaveInstance(ctx, in); err != nil {
		return nil, err
	}
	if in.Status != InstanceRunning && in.InitiatorID != nil {
		out = append(out, notification.CreateNotificationInput{
			Recipients: users([]string{*in.InitiatorID}),
			Type:       TypeWorkflowDecision,
			Title:      in.DefinitionName,
			Message:    fmt.Sprintf("%s: %s", in.DefinitionName, in.Status),
			Data:       s.data(in, map[string]any{"status": in.Status, "link": instanceLink(in.ID)}),
		})
	}
	return out, nil
}

func (s *Service) data(in *Instance, extra map[string]any) map[string]any {
	d := map[string]any{"instance_id": in.ID, "workflow": in.DefinitionName}
	if in.TargetName != nil {
		d["asset_name"] = *in.TargetName
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// CompleteTask records a decision on a task and runs the instance on.
// Deciding needs to be a candidate at this moment, not only when the task was
// created; native admins may decide any task. When the user task declares
// dgu:formFields, fields must supply every listed id and are written to the
// target asset before the flow advances.
func (s *Service) CompleteTask(ctx context.Context, p auth.Principal, taskID, decision, comment string, fields map[string]any) (*Instance, error) {
	userID, ok := principalUserID(p)
	if !ok {
		return nil, ErrForbidden
	}
	if decision != "" && !decisionRE.MatchString(decision) {
		return nil, fmt.Errorf("%w: decision must be a short word", ErrInvalidInput)
	}
	if len(comment) > maxCommentLength {
		return nil, fmt.Errorf("%w: comment is too long", ErrInvalidInput)
	}
	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var in *Instance
	var out pending
	err = s.repo.WithTx(ctx, func(tx *PostgresRepository) error {
		in, err = tx.GetInstance(ctx, task.InstanceID, true)
		if err != nil {
			return err
		}
		if in.Status != InstanceRunning {
			return ErrNotRunning
		}
		current, err := tx.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
		if current.Status != TaskOpen {
			return ErrConflict
		}
		def, err := tx.GetDefinition(ctx, in.DefinitionID)
		if err != nil {
			return err
		}
		proc, err := Parse([]byte(def.BPMN))
		if err != nil {
			return err
		}
		node := proc.Nodes[current.NodeID]
		if !p.IsAdmin() {
			candidates, err := s.candidates(ctx, node, in)
			if err != nil {
				return err
			}
			if !slices.Contains(candidates, userID) {
				return ErrForbidden
			}
		}
		if len(node.FormFields) > 0 {
			if err := s.writeFormFields(ctx, p, in, node.FormFields, fields); err != nil {
				return err
			}
		} else if len(fields) > 0 {
			return fmt.Errorf("%w: this task does not accept form fields", ErrInvalidInput)
		}
		vars := map[string]string{}
		if decision != "" {
			vars["decision"] = decision
			vars[current.NodeID+".decision"] = decision
		}
		participants := strings.Split(in.State.Vars["_participants"], ",")
		if !slices.Contains(participants, userID) {
			vars["_participants"] = strings.Trim(in.State.Vars["_participants"]+","+userID, ",")
		}
		var dec, com *string
		if decision != "" {
			dec = &decision
		}
		if comment != "" {
			com = &comment
		}
		if err := tx.CloseTask(ctx, in.ID, current.TokenID, TaskCompleted, dec, com, &userID); err != nil {
			return err
		}
		exec := &executor{svc: s, instance: in, principal: p}
		runner := &Runner{Process: proc, Executor: exec, Now: s.now}
		step, err := runner.Complete(ctx, in.State, current.TokenID, vars)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrConflict, err)
		}
		if decision != "" {
			step.Events[0].Detail = map[string]any{"decision": decision}
		}
		if len(fields) > 0 {
			if step.Events[0].Detail == nil {
				step.Events[0].Detail = map[string]any{}
			}
			step.Events[0].Detail["fields"] = fields
		}
		notes, err := s.apply(ctx, tx, in, step, &userID)
		out = append(append(out, exec.notes...), notes...)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.send(ctx, out)
	return in, nil
}

func (s *Service) writeFormFields(ctx context.Context, p auth.Principal, in *Instance, required []string, fields map[string]any) error {
	if in.TargetKind == nil || *in.TargetKind != TargetAsset || in.TargetID == nil {
		return fmt.Errorf("%w: the instance has no target asset for form fields", ErrInvalidInput)
	}
	if p == nil || (!p.IsAdmin() && !p.HasPermission("assets", "manage")) {
		return fmt.Errorf("%w: may not write asset fields", ErrForbidden)
	}
	if fields == nil {
		fields = map[string]any{}
	}
	patch := make(map[string]any, len(required))
	for _, id := range required {
		v, ok := fields[id]
		if !ok {
			return fmt.Errorf("%w: missing form field %q", ErrInvalidInput, id)
		}
		// HTML forms send strings; coerce JSON literals like set_field (true, 3, null).
		if s, ok := v.(string); ok {
			v = FieldValue(s)
		}
		patch[id] = v
	}
	for id := range fields {
		if !slices.Contains(required, id) {
			return fmt.Errorf("%w: unexpected form field %q", ErrInvalidInput, id)
		}
	}
	a, err := s.assets.Get(ctx, *in.TargetID)
	if err != nil {
		return err
	}
	_, err = s.assets.PatchFields(ctx, *in.TargetID, a.Version, patch)
	return err
}

// Cancel stops a running instance.
func (s *Service) Cancel(ctx context.Context, p auth.Principal, id string) (*Instance, error) {
	if !canManage(p) {
		return nil, ErrForbidden
	}
	var actor *string
	if uid, ok := principalUserID(p); ok {
		actor = &uid
	}
	var in *Instance
	err := s.repo.WithTx(ctx, func(tx *PostgresRepository) error {
		var err error
		in, err = tx.GetInstance(ctx, id, true)
		if err != nil {
			return err
		}
		if in.Status != InstanceRunning {
			return ErrNotRunning
		}
		if err := tx.CancelOpenTasks(ctx, in.ID); err != nil {
			return err
		}
		in.Status = InstanceCancelled
		in.State.Tokens = nil
		if err := tx.InsertEvents(ctx, in.ID, actor, []StepEvent{{Type: "cancelled"}}); err != nil {
			return err
		}
		return tx.SaveInstance(ctx, in)
	})
	return in, err
}

// InstanceDetail is an instance with its tasks and audit trail.
type InstanceDetail struct {
	*Instance
	Tasks  []*Task  `json:"tasks"`
	Events []*Event `json:"events"`
	BPMN   string   `json:"bpmn"`
	// Active lists the nodes that hold a token, to highlight in the diagram.
	Active []string `json:"active"`
}

func (s *Service) visible(ctx context.Context, p auth.Principal, in *Instance) (bool, error) {
	if canManage(p) {
		return true, nil
	}
	uid, ok := principalUserID(p)
	if !ok {
		return false, nil
	}
	if in.InitiatorID != nil && *in.InitiatorID == uid {
		return true, nil
	}
	tasks, err := s.repo.ListTasks(ctx, in.ID)
	if err != nil {
		return false, err
	}
	for _, t := range tasks {
		if slices.Contains(t.Candidates, uid) || (t.CompletedBy != nil && *t.CompletedBy == uid) {
			return true, nil
		}
	}
	return false, nil
}

// GetInstance returns an instance to a manager, its initiator, or someone
// who has or had a task in it. Anyone else gets ErrNotFound.
func (s *Service) GetInstance(ctx context.Context, p auth.Principal, id string) (*InstanceDetail, error) {
	in, err := s.repo.GetInstance(ctx, id, false)
	if err != nil {
		return nil, err
	}
	ok, err := s.visible(ctx, p, in)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	tasks, err := s.repo.ListTasks(ctx, id)
	if err != nil {
		return nil, err
	}
	s.attachFormFields(ctx, tasks)
	events, err := s.repo.ListEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	def, err := s.repo.GetDefinition(ctx, in.DefinitionID)
	if err != nil {
		return nil, err
	}
	active := []string{}
	for _, t := range in.State.Tokens {
		active = append(active, t.Node)
	}
	return &InstanceDetail{Instance: in, Tasks: tasks, Events: events, BPMN: def.BPMN, Active: active}, nil
}

// ListInstances lists what the caller may see; all asks for every instance
// and needs the manage permission.
func (s *Service) ListInstances(ctx context.Context, p auth.Principal, f InstanceFilter, all bool) ([]*Instance, error) {
	if all {
		if !canManage(p) {
			return nil, ErrForbidden
		}
		f.Participant = ""
	} else {
		uid, ok := principalUserID(p)
		if !ok {
			return []*Instance{}, nil
		}
		f.Participant = uid
	}
	return s.repo.ListInstances(ctx, f)
}

// MyTasks lists the open tasks the caller is a candidate for.
func (s *Service) MyTasks(ctx context.Context, p auth.Principal) ([]*Task, error) {
	uid, ok := principalUserID(p)
	if !ok {
		return []*Task{}, nil
	}
	tasks, err := s.repo.OpenTasksFor(ctx, uid)
	if err != nil {
		return nil, err
	}
	s.attachFormFields(ctx, tasks)
	return tasks, nil
}

func (s *Service) attachFormFields(ctx context.Context, tasks []*Task) {
	cache := map[string]*Process{}
	for _, task := range tasks {
		in, err := s.repo.GetInstance(ctx, task.InstanceID, false)
		if err != nil {
			continue
		}
		proc, ok := cache[in.DefinitionID]
		if !ok {
			def, err := s.repo.GetDefinition(ctx, in.DefinitionID)
			if err != nil {
				continue
			}
			parsed, err := Parse([]byte(def.BPMN))
			if err != nil {
				continue
			}
			cache[in.DefinitionID] = parsed
			proc = parsed
		}
		if node := proc.Nodes[task.NodeID]; node != nil && len(node.FormFields) > 0 {
			task.FormFields = append([]string{}, node.FormFields...)
		}
	}
}

// RunTimers fires the boundary timers that are due. Actions reached from a
// timer run with no user identity, so a timer path cannot write assets.
func (s *Service) RunTimers(ctx context.Context) error {
	due, err := s.repo.DueTimers(ctx, s.now(), timerBatch)
	if err != nil {
		return err
	}
	for _, task := range due {
		if err := s.fireTimer(ctx, task); err != nil && !errors.Is(err, ErrNotRunning) && !errors.Is(err, ErrConflict) {
			log.Error().Err(err).Str("task_id", task.ID).Msg("Workflow timer failed")
		}
	}
	return nil
}

func (s *Service) fireTimer(ctx context.Context, task *Task) error {
	var out pending
	err := s.repo.WithTx(ctx, func(tx *PostgresRepository) error {
		in, err := tx.GetInstance(ctx, task.InstanceID, true)
		if err != nil {
			return err
		}
		if err := tx.MarkTimerFired(ctx, task.ID); err != nil {
			return err
		}
		if in.Status != InstanceRunning {
			return nil
		}
		current, err := tx.GetTask(ctx, task.ID)
		if err != nil || current.Status != TaskOpen || current.TimerNode == nil {
			return err
		}
		def, err := tx.GetDefinition(ctx, in.DefinitionID)
		if err != nil {
			return err
		}
		proc, err := Parse([]byte(def.BPMN))
		if err != nil {
			return err
		}
		exec := &executor{svc: s, instance: in}
		runner := &Runner{Process: proc, Executor: exec, Now: s.now}
		step, err := runner.FireTimer(ctx, in.State, current.TokenID, *current.TimerNode)
		if err != nil {
			return nil
		}
		notes, err := s.apply(ctx, tx, in, step, nil)
		out = append(out, exec.notes...)
		for _, n := range notes {
			if n.Type == TypeTaskAssigned {
				n.Type = TypeTaskEscalated
			}
			out = append(out, n)
		}
		return err
	})
	if err != nil {
		return err
	}
	s.send(ctx, out)
	return nil
}

// candidates resolves who may work on a user task, as user ids.
func (s *Service) candidates(ctx context.Context, node *Node, in *Instance) ([]string, error) {
	var out []string
	add := func(ids ...string) {
		for _, id := range ids {
			if id != "" && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	names := append([]string{}, node.CandidateUsers...)
	if node.Assignee != "" {
		names = append(names, node.Assignee)
	}
	for _, name := range names {
		if id := s.userID(ctx, name, in); id != "" {
			add(id)
		}
	}
	for _, raw := range node.CandidateGroups {
		g, err := ParseGroup(raw)
		if err != nil {
			continue
		}
		ids, err := s.groupMembers(ctx, g, in)
		if err != nil {
			return nil, err
		}
		add(ids...)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func (s *Service) userID(ctx context.Context, ref string, in *Instance) string {
	if ref == "${initiator}" && in.InitiatorID != nil {
		return *in.InitiatorID
	}
	var u *user.User
	var err error
	if _, parseErr := uuid.Parse(ref); parseErr == nil {
		u, err = s.users.Get(ctx, ref)
	} else {
		u, err = s.users.GetUserByUsername(ctx, ref)
	}
	if err != nil || u == nil || !u.Active {
		return ""
	}
	return u.ID
}

func (s *Service) teamMembers(ctx context.Context, ref string) ([]string, error) {
	var t *team.Team
	var err error
	if _, parseErr := uuid.Parse(ref); parseErr == nil {
		t, err = s.teams.GetTeam(ctx, ref)
	} else {
		t, err = s.teams.GetTeamByName(ctx, ref)
	}
	if err != nil || t == nil {
		return nil, nil
	}
	members, err := s.teams.ListMembers(ctx, t.ID)
	if err != nil {
		return nil, fmt.Errorf("listing team members: %w", err)
	}
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, m.UserID)
	}
	return out, nil
}

func (s *Service) groupMembers(ctx context.Context, g Group, in *Instance) ([]string, error) {
	if g.Kind == GroupTeam {
		return s.teamMembers(ctx, g.Value)
	}
	if s.domains == nil {
		return nil, nil
	}
	domainID := g.DomainID
	if domainID == "" {
		if in.TargetKind == nil || in.TargetID == nil {
			return nil, nil
		}
		id, err := s.domains.DomainOf(ctx, domain.Kind(*in.TargetKind), *in.TargetID)
		if err != nil {
			return nil, nil
		}
		domainID = id
	}
	roles, err := s.domains.Roles(ctx, domainID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing domain roles: %w", err)
	}
	var out []string
	for _, ra := range roles {
		if string(ra.Role) != g.Value || ra.SubjectMissing {
			continue
		}
		switch ra.SubjectType {
		case domain.SubjectUser:
			out = append(out, ra.SubjectID)
		case domain.SubjectTeam:
			ids, err := s.teamMembers(ctx, ra.SubjectID)
			if err != nil {
				return nil, err
			}
			out = append(out, ids...)
		}
	}
	return out, nil
}

// executor runs service task actions for one instance, as the principal who
// advanced it. Notifications it produces wait for the commit.
type executor struct {
	svc       *Service
	instance  *Instance
	principal auth.Principal
	notes     pending
}

func (e *executor) Execute(ctx context.Context, n *Node, vars map[string]string) error {
	switch n.Action {
	case ActionNotify:
		ids := []string{}
		if e.instance.InitiatorID != nil {
			ids = append(ids, *e.instance.InitiatorID)
		}
		if n.Args["to"] == "participants" {
			for _, id := range strings.Split(vars["_participants"], ",") {
				if id != "" && !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
			}
		}
		e.notes = append(e.notes, notification.CreateNotificationInput{
			Recipients: users(ids),
			Type:       TypeWorkflowMessage,
			Title:      e.instance.DefinitionName,
			Message:    n.Args["message"],
			Data:       e.svc.data(e.instance, map[string]any{"link": instanceLink(e.instance.ID)}),
		})
		return nil
	case ActionSetField, ActionClearField, ActionAddTag, ActionRemoveTag:
		if e.instance.TargetKind == nil || *e.instance.TargetKind != TargetAsset || e.instance.TargetID == nil {
			return errors.New("the instance has no target asset")
		}
		if e.principal == nil {
			return errors.New("no user identity to write the asset with")
		}
		if !e.principal.IsAdmin() && !e.principal.HasPermission("assets", "manage") {
			return fmt.Errorf("%s may not manage assets", e.principal.AuditSubject())
		}
		id := *e.instance.TargetID
		switch n.Action {
		case ActionAddTag:
			_, err := e.svc.assets.AddTag(ctx, id, strings.TrimSpace(n.Args["tag"]))
			return err
		case ActionRemoveTag:
			_, err := e.svc.assets.RemoveTag(ctx, id, strings.TrimSpace(n.Args["tag"]))
			return err
		}
		a, err := e.svc.assets.Get(ctx, id)
		if err != nil {
			return err
		}
		var patch map[string]any
		if n.Action == ActionClearField {
			patch = map[string]any{n.Args["field"]: nil}
		} else {
			patch = map[string]any{n.Args["field"]: FieldValue(n.Args["value"])}
		}
		_, err = e.svc.assets.PatchFields(ctx, id, a.Version, patch)
		return err
	}
	return fmt.Errorf("unknown action %q", n.Action)
}

// SetClock replaces the service clock, for tests that move time forward.
func SetClock(s *Service, now func() time.Time) { s.now = now }
