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
	"github.com/marmotdata/marmot/internal/core/glossary"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/notification"
	"github.com/marmotdata/marmot/internal/core/team"
	"github.com/marmotdata/marmot/internal/core/user"
	"github.com/rs/zerolog/log"
)

// Notification types the engine emits.
const (
	TypeTaskAssigned     = "task_assigned"
	TypeTaskEscalated    = "task_escalated"
	TypeTaskDue          = "task_due"
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
	AddTerms(ctx context.Context, assetID string, termIDs []string, source, createdBy string) error
	RemoveTerm(ctx context.Context, assetID, termID string) error
	Metamodel(kind string) metamodel.Schema
}

// Glossary resolves the term an action names. Nil disables the term actions.
type Glossary interface {
	GetByName(ctx context.Context, name string) (*glossary.GlossaryTerm, error)
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
	glossary Glossary
	notifier Notifier
	now      func() time.Time
	// withPrincipal carries a person into the context the write guards read.
	// Without it a timer path has no identity.
	withPrincipal func(context.Context, auth.Principal) context.Context
}

func NewService(repo *PostgresRepository, users Users, teams Teams, domains Domains, assets Assets, notifier Notifier) *Service {
	return &Service{repo: repo, users: users, teams: teams, domains: domains, assets: assets, notifier: notifier, now: time.Now}
}

// Capabilities is what this engine runs, so a client lists it instead of
// guessing from the version or from a probe request.
type Capabilities struct {
	Actions  []ActionSpec `json:"actions"`
	Features []string     `json:"features"`
	// CanStart and CanManage are about the caller: whether it may start runs and manage definitions.
	CanStart  bool `json:"can_start"`
	CanManage bool `json:"can_manage"`
}

// Capabilities reports the action catalogue, the optional features in use and what p may do.
func (s *Service) Capabilities(p auth.Principal) Capabilities {
	features := []string{"boundary_timer", "wait_timer", "timer_start", "task_reminder", "form_fields", "notify_groups"}
	if s.queries != nil {
		features = append(features, "batch_start")
	}
	actions := make([]ActionSpec, 0, len(Actions))
	for _, spec := range Actions {
		if (spec.ID == ActionLinkTerm || spec.ID == ActionUnlinkTerm) && s.glossary == nil {
			continue
		}
		actions = append(actions, spec)
	}
	return Capabilities{Actions: actions, Features: features, CanStart: canStart(p), CanManage: canManage(p)}
}

// WithPrincipalContext lets timer paths and scheduled starts act as a person: fn returns a
// context in which the write guards see that person.
func (s *Service) WithPrincipalContext(fn func(context.Context, auth.Principal) context.Context) *Service {
	s.withPrincipal = fn
	return s
}

// actingAs loads the person a path without a request acts as, with the permissions they have
// now. A missing, inactive or unknown user yields no identity, so the action fails visibly.
func (s *Service) actingAs(ctx context.Context, userID *string) (auth.Principal, context.Context) {
	if userID == nil || s.withPrincipal == nil {
		return nil, ctx
	}
	u, err := s.users.Get(ctx, *userID)
	if err != nil || u == nil || !u.Active {
		return nil, ctx
	}
	p := auth.NewUserPrincipal(u)
	return p, s.withPrincipal(ctx, p)
}

// WithGlossary enables the actions that link a glossary term to the target asset.
func (s *Service) WithGlossary(g Glossary) *Service {
	s.glossary = g
	return s
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

func canStart(p auth.Principal) bool {
	return p != nil && (p.IsAdmin() || p.HasPermission("workflows", "start"))
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
	if err == nil {
		s.attachSchedules(ctx, []*Definition{d})
	}
	return withIssues(d), err
}

// attachSchedules fills the timer start of the definitions that have one. A failed lookup
// leaves the list without it: the schedule is information, not a reason to fail a read.
func (s *Service) attachSchedules(ctx context.Context, defs []*Definition) {
	ids := make([]string, 0, len(defs))
	for _, d := range defs {
		ids = append(ids, d.ID)
	}
	found, err := s.repo.SchedulesOf(ctx, ids)
	if err != nil {
		log.Warn().Err(err).Msg("Workflow schedules unavailable")
		return
	}
	for _, d := range defs {
		d.Schedule = found[d.ID]
	}
}

// ListDefinitions lists every version to managers and only published ones to
// everybody else.
func (s *Service) ListDefinitions(ctx context.Context, p auth.Principal) ([]*Definition, error) {
	status := StatusPublished
	if canManage(p) {
		status = ""
	}
	defs, err := s.repo.ListDefinitions(ctx, status)
	if err != nil {
		return nil, err
	}
	s.attachSchedules(ctx, defs)
	return defs, nil
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
	proc, err := Parse([]byte(d.BPMN))
	if err != nil {
		return nil, err
	}
	publisher, isUser := principalUserID(p)
	if proc.Schedule != nil && !isUser {
		// A scheduled start acts as a person, and only a person can be that.
		return nil, ErrForbidden
	}
	var published *Definition
	err = s.repo.WithTx(ctx, func(tx *PostgresRepository) error {
		var err error
		published, err = tx.SetDefinitionStatus(ctx, id, StatusDraft, StatusPublished)
		if err != nil || proc.Schedule == nil {
			return err
		}
		cycle, err := ParseCycle(proc.Schedule.Cycle)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		return tx.UpsertSchedule(ctx, id, published.ProcessKey, proc.Schedule.Cycle, proc.Schedule.Query, publisher, cycle.Next(s.now()))
	})
	if err != nil {
		return nil, err
	}
	s.attachSchedules(ctx, []*Definition{published})
	return withIssues(published), nil
}

// Retire stops new runs of a published version; running ones go on.
func (s *Service) Retire(ctx context.Context, p auth.Principal, id string) (*Definition, error) {
	if !canManage(p) {
		return nil, ErrForbidden
	}
	var retired *Definition
	err := s.repo.WithTx(ctx, func(tx *PostgresRepository) error {
		var err error
		retired, err = tx.SetDefinitionStatus(ctx, id, StatusPublished, StatusRetired)
		if err != nil {
			return err
		}
		return tx.DisableSchedule(ctx, id)
	})
	if err != nil {
		return nil, err
	}
	s.attachSchedules(ctx, []*Definition{retired})
	return withIssues(retired), nil
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
	if !ok || !canStart(p) {
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
	// Failed lists the matches that could not start; the others did.
	Failed []StartFailure `json:"failed,omitempty"`
}

// StartFailure is one asset of a batch that did not start, with a stable code.
type StartFailure struct {
	AssetID string `json:"asset_id"`
	Code    string `json:"code"`
}

func startFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, ErrInvalidInput):
		return "invalid_input"
	default:
		return "failed"
	}
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
		switch {
		case err == nil:
			out.Instances = append(out.Instances, in)
			out.Started++
		case errors.Is(err, ErrNotRunnable), errors.Is(err, ErrNotFound), ctx.Err() != nil:
			// Nothing about the asset explains it: every remaining start would fail the same way.
			return out, err
		default:
			log.Warn().Err(err).Str("asset_id", id).Msg("Workflow batch start skipped an asset")
			out.Failed = append(out.Failed, StartFailure{AssetID: id, Code: startFailureCode(err)})
		}
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
	for _, token := range step.ReleasedWaits {
		if err := tx.CloseTask(ctx, in.ID, token, TaskCompleted, nil, nil, nil); err != nil && !errors.Is(err, ErrConflict) {
			return nil, err
		}
	}
	for _, nt := range step.NewTasks {
		if nt.Node.Type == NodeWait {
			node := nt.Node.ID
			wait := &Task{InstanceID: in.ID, TokenID: nt.TokenID, NodeID: node, Name: displayName(node, nt.Node.Name), Candidates: []string{}, DueAt: nt.DueAt, TimerNode: &node}
			if err := tx.InsertTask(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		candidates, err := s.candidates(ctx, nt.Node, in)
		if err != nil {
			return nil, err
		}
		t := &Task{InstanceID: in.ID, TokenID: nt.TokenID, NodeID: nt.Node.ID, Name: displayName(nt.Node.ID, nt.Node.Name), Candidates: candidates, DueAt: nt.DueAt, RemindAt: nt.RemindAt, FormFields: nt.Node.FormFields}
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
		if node == nil || node.Type != NodeUserTask {
			return fmt.Errorf("%w: only a user task can be decided", ErrInvalidInput)
		}
		if !p.IsAdmin() {
			candidates, err := s.candidates(ctx, node, in)
			if err != nil {
				return err
			}
			if !slices.Contains(candidates, userID) {
				return ErrForbidden
			}
		}
		switch {
		case len(node.FormFields) == 0 && len(fields) > 0:
			return fmt.Errorf("%w: this task does not accept form fields", ErrInvalidInput)
		case len(node.FormFields) > 0 && isRejection(decision):
			if len(fields) > 0 {
				return fmt.Errorf("%w: fields are not written when a task is rejected", ErrInvalidInput)
			}
		case len(node.FormFields) > 0:
			if err := s.writeFormFields(ctx, p, in, node.FormFields, fields); err != nil {
				return err
			}
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

// rejections are the decisions that end a task without the changes its form asks for.
var rejections = []string{"rejected", "reject", "denied", "declined"}

func isRejection(decision string) bool {
	return slices.Contains(rejections, strings.ToLower(decision))
}

// coerceField turns the text a form or a dgu:value carries into what the
// field's declared type expects. The asset API stays strict: only the workflow
// accepts text for a number, a boolean or a list. An unknown field passes
// through, so PatchFields reports it as it would for any client.
func (s *Service) coerceField(id string, value any) (any, error) {
	for _, field := range s.assets.Metamodel("asset").Fields {
		if field.ID != id {
			continue
		}
		coerced, ok := metamodel.Coerce(field, value)
		if !ok {
			return nil, &metamodel.ValidationError{Fields: []metamodel.Violation{{Field: id, Code: "type"}}}
		}
		return coerced, nil
	}
	return value, nil
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
		coerced, err := s.coerceField(id, v)
		if err != nil {
			return err
		}
		patch[id] = coerced
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
	for _, t := range tasks {
		t.Wait = t.TimerNode != nil && *t.TimerNode == t.NodeID
	}
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
	return tasks, nil
}

// RunTimers fires the boundary timers and waits that are due, then sends the
// reminders that are. Actions reached from a timer act as the run's initiator,
// with the permissions that person has when it fires.
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
	return errors.Join(s.remind(ctx), s.runSchedules(ctx))
}

// runSchedules starts the definitions whose timer start is due.
func (s *Service) runSchedules(ctx context.Context) error {
	var due []*Schedule
	err := s.repo.WithTx(ctx, func(tx *PostgresRepository) error {
		var err error
		due, err = tx.ClaimSchedules(ctx, s.now(), timerBatch)
		return err
	})
	if err != nil {
		return err
	}
	for _, sc := range due {
		s.startScheduled(ctx, sc)
	}
	return nil
}

// startScheduled starts one scheduled definition as the person who published it and records
// how it went. A publisher who is gone, or who lost the right to start, stops the runs
// visibly: last_error says why, and nothing borrows another identity.
func (s *Service) startScheduled(ctx context.Context, sc *Schedule) {
	failure := ""
	defer func() {
		if err := s.repo.SetScheduleResult(ctx, sc.DefinitionID, failure); err != nil {
			log.Warn().Err(err).Str("definition_id", sc.DefinitionID).Msg("Workflow schedule result not stored")
		}
	}()
	def, err := s.repo.GetDefinition(ctx, sc.DefinitionID)
	if err != nil || def.Status != StatusPublished {
		failure = "the definition is no longer published"
		_ = s.repo.DisableSchedule(ctx, sc.DefinitionID)
		return
	}
	var runAs *string
	if sc.RunAs != "" {
		runAs = &sc.RunAs
	}
	person, actCtx := s.actingAs(ctx, runAs)
	if person == nil {
		failure = "the person who published it can no longer act"
		return
	}
	if sc.Query == "" {
		if _, err := s.Start(actCtx, person, sc.DefinitionID, nil); err != nil {
			failure = err.Error()
		}
		return
	}
	batch, err := s.StartQuery(actCtx, person, sc.DefinitionID, sc.Query, maxStartBatch)
	switch {
	case err != nil:
		failure = err.Error()
	case len(batch.Failed) > 0:
		failure = fmt.Sprintf("%d assets could not start", len(batch.Failed))
	}
}

// remind tells the candidates of each task whose reminder time has come that it is due soon.
// Marking the task first makes a reminder at most once, even across replicas.
func (s *Service) remind(ctx context.Context) error {
	tasks, err := s.repo.DueReminders(ctx, s.now(), timerBatch)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		marked, err := s.repo.MarkReminded(ctx, task.ID)
		if err != nil || !marked || len(task.Candidates) == 0 {
			continue
		}
		in, err := s.repo.GetInstance(ctx, task.InstanceID, false)
		if err != nil || in.Status != InstanceRunning {
			continue
		}
		data := map[string]any{"task_id": task.ID, "link": tasksLink}
		if task.DueAt != nil {
			data["due_at"] = task.DueAt.UTC().Format(time.RFC3339)
		}
		s.send(ctx, pending{{
			Recipients: users(task.Candidates),
			Type:       TypeTaskDue,
			Title:      task.Name,
			Message:    fmt.Sprintf("%s: %s is due soon", in.DefinitionName, task.Name),
			Data:       s.data(in, data),
		}})
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
		person, actCtx := s.actingAs(ctx, in.InitiatorID)
		exec := &executor{svc: s, instance: in, principal: person}
		runner := &Runner{Process: proc, Executor: exec, Now: s.now}
		step, err := runner.FireTimer(actCtx, in.State, current.TokenID, *current.TimerNode)
		if err != nil {
			return nil
		}
		notes, err := s.apply(ctx, tx, in, step, nil)
		out = append(out, exec.notes...)
		escalated := proc.Nodes[*current.TimerNode].Type == NodeBoundaryEvent
		for _, n := range notes {
			if escalated && n.Type == TypeTaskAssigned {
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

// linkTerm links or unlinks the glossary term dgu:term names. The name is resolved when the
// action runs, so a renamed or deleted term fails the run visibly instead of linking another.
func (e *executor) linkTerm(ctx context.Context, assetID string, n *Node) error {
	if e.svc.glossary == nil {
		return errors.New("glossary actions are not available")
	}
	name := strings.TrimSpace(n.Args["term"])
	term, err := e.svc.glossary.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("glossary term %q: %w", name, err)
	}
	if n.Action == ActionUnlinkTerm {
		return e.svc.assets.RemoveTerm(ctx, assetID, term.ID)
	}
	return e.svc.assets.AddTerms(ctx, assetID, []string{term.ID}, "workflow", e.principal.ID())
}

// recipients resolves a dgu:to list to user ids, once, when the action runs.
func (e *executor) recipients(ctx context.Context, spec string, vars map[string]string) ([]string, error) {
	entries := splitList(spec)
	if len(entries) == 0 {
		entries = []string{RecipientInitiator}
	}
	ids := []string{}
	add := func(candidates ...string) {
		for _, id := range candidates {
			if id != "" && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	for _, entry := range entries {
		switch entry {
		case RecipientInitiator:
			if e.instance.InitiatorID != nil {
				add(*e.instance.InitiatorID)
			}
		case RecipientParticipants:
			if e.instance.InitiatorID != nil {
				add(*e.instance.InitiatorID)
			}
			add(strings.Split(vars["_participants"], ",")...)
		default:
			group, err := ParseGroup(entry)
			if err != nil {
				return nil, err
			}
			members, err := e.svc.groupMembers(ctx, group, e.instance)
			if err != nil {
				return nil, err
			}
			add(members...)
		}
	}
	return ids, nil
}

func (e *executor) Execute(ctx context.Context, n *Node, vars map[string]string) error {
	switch n.Action {
	case ActionNotify:
		ids, err := e.recipients(ctx, n.Args["to"], vars)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		e.notes = append(e.notes, notification.CreateNotificationInput{
			Recipients: users(ids),
			Type:       TypeWorkflowMessage,
			Title:      e.instance.DefinitionName,
			Message:    n.Args["message"],
			Data:       e.svc.data(e.instance, map[string]any{"link": instanceLink(e.instance.ID)}),
		})
		return nil
	case ActionSetField, ActionClearField, ActionAddTag, ActionRemoveTag, ActionLinkTerm, ActionUnlinkTerm:
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
		case ActionLinkTerm, ActionUnlinkTerm:
			return e.linkTerm(ctx, id, n)
		}
		a, err := e.svc.assets.Get(ctx, id)
		if err != nil {
			return err
		}
		var patch map[string]any
		if n.Action == ActionClearField {
			patch = map[string]any{n.Args["field"]: nil}
		} else {
			value, err := e.svc.coerceField(n.Args["field"], n.Args["value"])
			if err != nil {
				return err
			}
			patch = map[string]any{n.Args["field"]: value}
		}
		_, err = e.svc.assets.PatchFields(ctx, id, a.Version, patch)
		return err
	}
	return fmt.Errorf("unknown action %q", n.Action)
}

// SetClock replaces the service clock, for tests that move time forward.
func SetClock(s *Service, now func() time.Time) { s.now = now }
