package workflow_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/auth"
	"github.com/marmotdata/marmot/internal/core/domain"
	"github.com/marmotdata/marmot/internal/core/notification"
	"github.com/marmotdata/marmot/internal/core/team"
	"github.com/marmotdata/marmot/internal/core/user"
	"github.com/marmotdata/marmot/internal/core/workflow"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

const doc = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:camunda="http://camunda.org/schema/1.0/bpmn" xmlns:dgu="https://dgu-development.github.io/schema/workflows" id="d">
<bpmn:process id="term_approval" name="Term approval">
<bpmn:startEvent id="start"/>
<bpmn:userTask id="review" name="Review" camunda:candidateGroups="role:steward"/>
<bpmn:boundaryEvent id="late" attachedToRef="review"><bpmn:timerEventDefinition><bpmn:timeDuration>P2D</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:boundaryEvent>
<bpmn:userTask id="escalated" name="Escalated review" camunda:candidateGroups="role:domain_admin"/>
<bpmn:exclusiveGateway id="gw" default="no"/>
<bpmn:serviceTask id="activate" dgu:action="set_field" dgu:field="lifecycle" dgu:value="active"/>
<bpmn:serviceTask id="tell" dgu:action="notify" dgu:message="Approved"/>
<bpmn:endEvent id="ok"/>
<bpmn:endEvent id="ko"/>
<bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
<bpmn:sequenceFlow id="f2" sourceRef="review" targetRef="gw"/>
<bpmn:sequenceFlow id="f3" sourceRef="late" targetRef="escalated"/>
<bpmn:sequenceFlow id="f4" sourceRef="escalated" targetRef="gw"/>
<bpmn:sequenceFlow id="yes" sourceRef="gw" targetRef="activate"><bpmn:conditionExpression>decision == "approved"</bpmn:conditionExpression></bpmn:sequenceFlow>
<bpmn:sequenceFlow id="no" sourceRef="gw" targetRef="ko"/>
<bpmn:sequenceFlow id="f5" sourceRef="activate" targetRef="tell"/>
<bpmn:sequenceFlow id="f6" sourceRef="tell" targetRef="ok"/>
</bpmn:process></bpmn:definitions>`

const assetID = "11111111-1111-4111-8111-111111111111"
const domainID = "22222222-2222-4222-8222-222222222222"

type fakeUsers struct{ byID map[string]*user.User }

func (f *fakeUsers) Get(_ context.Context, id string) (*user.User, error) {
	if u, ok := f.byID[id]; ok {
		return u, nil
	}
	return nil, user.ErrUserNotFound
}

func (f *fakeUsers) GetUserByUsername(_ context.Context, name string) (*user.User, error) {
	for _, u := range f.byID {
		if u.Username == name {
			return u, nil
		}
	}
	return nil, user.ErrUserNotFound
}

type fakeTeams struct{}

func (fakeTeams) GetTeam(context.Context, string) (*team.Team, error) { return nil, errors.New("none") }
func (fakeTeams) GetTeamByName(context.Context, string) (*team.Team, error) {
	return nil, errors.New("none")
}
func (fakeTeams) ListMembers(context.Context, string) ([]*team.TeamMemberWithUser, error) {
	return nil, nil
}

type fakeDomains struct {
	mu    sync.Mutex
	roles []domain.RoleAssignment
}

func (f *fakeDomains) DomainOf(context.Context, domain.Kind, string) (string, error) {
	return domainID, nil
}

func (f *fakeDomains) Roles(context.Context, string) ([]domain.RoleAssignment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.RoleAssignment{}, f.roles...), nil
}

func (f *fakeDomains) set(roles ...domain.RoleAssignment) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.roles = roles
}

type fakeAssets struct {
	patched    map[string]any
	removedTag string
	fail       error
}

func (f *fakeAssets) Get(_ context.Context, id string) (*asset.Asset, error) {
	if id != assetID {
		return nil, asset.ErrNotFound
	}
	name := "customers"
	return &asset.Asset{ID: id, Name: &name, Version: 3}, nil
}

func (f *fakeAssets) PatchFields(_ context.Context, _ string, version int64, fields map[string]any) (*asset.Asset, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	if version != 3 {
		return nil, errors.New("stale version")
	}
	if f.patched == nil {
		f.patched = map[string]any{}
	}
	for k, v := range fields {
		f.patched[k] = v
	}
	return &asset.Asset{}, nil
}

func (f *fakeAssets) AddTag(context.Context, string, string) (*asset.Asset, error) {
	return &asset.Asset{}, nil
}

func (f *fakeAssets) RemoveTag(_ context.Context, _ string, tag string) (*asset.Asset, error) {
	f.removedTag = tag
	return &asset.Asset{}, nil
}

type fakeNotifier struct {
	mu   sync.Mutex
	sent []notification.CreateNotificationInput
}

func (f *fakeNotifier) Create(_ context.Context, n notification.CreateNotificationInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
	return nil
}

func (f *fakeNotifier) types(recipient string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, n := range f.sent {
		for _, r := range n.Recipients {
			if r.ID == recipient {
				out = append(out, n.Type)
			}
		}
	}
	return out
}

type fixture struct {
	svc                              *workflow.Service
	pool                             *pgxpool.Pool
	domains                          *fakeDomains
	assets                           *fakeAssets
	notes                            *fakeNotifier
	now                              *time.Time
	admin, alice, bob, carol         auth.Principal
	adminID, aliceID, bobID, carolID string
}

var manageAssets = user.Role{Name: "user", Permissions: []user.Permission{{ResourceType: "assets", Action: "manage"}, {ResourceType: "workflows", Action: "view"}}}

func setup(t *testing.T) *fixture {
	t.Helper()
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	f := &fixture{pool: pool, domains: &fakeDomains{}, assets: &fakeAssets{}, notes: &fakeNotifier{}}
	users := &fakeUsers{byID: map[string]*user.User{}}
	mk := func(name string, roles ...user.Role) (auth.Principal, string) {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users (username, name) VALUES ($1, $1) RETURNING id::text`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		u := &user.User{ID: id, Username: name, Active: true, Roles: roles}
		users.byID[id] = u
		return auth.NewUserPrincipal(u), id
	}
	f.admin, f.adminID = mk("wf-admin", user.Role{Name: auth.AdminRoleName})
	f.alice, f.aliceID = mk("alice", manageAssets)
	f.bob, f.bobID = mk("bob", manageAssets)
	f.carol, f.carolID = mk("carol", manageAssets)
	f.domains.set(domain.RoleAssignment{SubjectType: domain.SubjectUser, SubjectID: f.bobID, Role: domain.RoleSteward})
	f.svc = workflow.NewService(workflow.NewPostgresRepository(pool), users, fakeTeams{}, f.domains, f.assets, f.notes)
	now := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	f.now = &now
	workflow.SetClock(f.svc, func() time.Time { return *f.now })
	return f
}

func (f *fixture) published(t *testing.T, bpmn string) *workflow.Definition {
	t.Helper()
	ctx := context.Background()
	d, err := f.svc.CreateDefinition(ctx, f.admin, []byte(bpmn))
	if err != nil {
		t.Fatal(err)
	}
	d, err = f.svc.Publish(ctx, f.admin, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func openTask(t *testing.T, f *fixture, p auth.Principal) *workflow.Task {
	t.Helper()
	tasks, err := f.svc.MyTasks(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v, want one", tasks)
	}
	return tasks[0]
}

func TestApprovalRunsToTheEndAndWritesTheAsset(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	def := f.published(t, doc)

	in, err := f.svc.Start(ctx, f.alice, def.ID, &workflow.Target{Kind: "asset", ID: assetID})
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != workflow.InstanceRunning || *in.TargetName != "customers" {
		t.Fatalf("instance = %+v", in)
	}
	task := openTask(t, f, f.bob)
	if task.DueAt == nil || !task.DueAt.Equal(f.now.Add(48*time.Hour)) || task.TargetName == nil {
		t.Fatalf("task = %+v", task)
	}
	if tasks, _ := f.svc.MyTasks(ctx, f.carol); len(tasks) != 0 {
		t.Fatalf("carol sees %+v", tasks)
	}

	if _, err := f.svc.CompleteTask(ctx, f.carol, task.ID, "approved", "", nil); !errors.Is(err, workflow.ErrForbidden) {
		t.Fatalf("carol decided: %v", err)
	}
	done, err := f.svc.CompleteTask(ctx, f.bob, task.ID, "approved", "Looks right", nil)
	if err != nil {
		t.Fatal(err)
	}

	if done.Status != workflow.InstanceCompleted {
		t.Fatalf("status = %s", done.Status)
	}
	if f.assets.patched["lifecycle"] != "active" {
		t.Fatalf("patched = %v", f.assets.patched)
	}
	if got := f.notes.types(f.bobID); len(got) != 1 || got[0] != workflow.TypeTaskAssigned {
		t.Fatalf("bob notified %v", got)
	}
	if got := strings.Join(f.notes.types(f.aliceID), ","); got != "workflow_message,workflow_decision" {
		t.Fatalf("alice notified %s", got)
	}
	detail, err := f.svc.GetInstance(ctx, f.alice, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Tasks[0].Status != workflow.TaskCompleted || *detail.Tasks[0].Decision != "approved" || *detail.Tasks[0].Comment != "Looks right" {
		t.Fatalf("task = %+v", detail.Tasks[0])
	}
	var types []string
	for _, e := range detail.Events {
		types = append(types, e.Type)
	}
	if strings.Join(types, ",") != "started,task_created,task_completed,branch_taken,action_done,action_done,ended,completed" {
		t.Fatalf("events = %v", types)
	}
	if _, err := f.svc.CompleteTask(ctx, f.bob, task.ID, "approved", "", nil); !errors.Is(err, workflow.ErrNotRunning) {
		t.Fatalf("second decision: %v", err)
	}
}

func TestARevokedRoleCannotDecide(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	def := f.published(t, doc)
	if _, err := f.svc.Start(ctx, f.alice, def.ID, &workflow.Target{Kind: "asset", ID: assetID}); err != nil {
		t.Fatal(err)
	}
	task := openTask(t, f, f.bob)

	f.domains.set()

	if _, err := f.svc.CompleteTask(ctx, f.bob, task.ID, "approved", "", nil); !errors.Is(err, workflow.ErrForbidden) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.svc.CompleteTask(ctx, f.admin, task.ID, "rejected", "", nil); err != nil {
		t.Fatalf("a native admin could not decide: %v", err)
	}
}

func TestDraftsSaveWithIssuesButOnlyValidOnesPublish(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	broken := strings.Replace(doc, `camunda:candidateGroups="role:steward"`, ``, 1)

	if _, err := f.svc.CreateDefinition(ctx, f.alice, []byte(doc)); !errors.Is(err, workflow.ErrForbidden) {
		t.Fatalf("a user created a definition: %v", err)
	}
	d, err := f.svc.CreateDefinition(ctx, f.admin, []byte(broken))
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 1 || d.Status != workflow.StatusDraft || len(d.Issues) != 1 || d.Issues[0].Code != "task_needs_assignment" {
		t.Fatalf("draft = %+v", d)
	}
	if _, err := f.svc.Publish(ctx, f.admin, d.ID); !errors.Is(err, workflow.ErrInvalidDiagram) {
		t.Fatalf("publish broken: %v", err)
	}
	if _, err := f.svc.Start(ctx, f.alice, d.ID, nil); !errors.Is(err, workflow.ErrNotRunnable) {
		t.Fatalf("start draft: %v", err)
	}
	renamed := strings.Replace(doc, `id="term_approval"`, `id="other"`, 1)
	if _, err := f.svc.UpdateDefinition(ctx, f.admin, d.ID, []byte(renamed)); !errors.Is(err, workflow.ErrInvalidInput) {
		t.Fatalf("process id change: %v", err)
	}
	d, err = f.svc.UpdateDefinition(ctx, f.admin, d.ID, []byte(doc))
	if err != nil || len(d.Issues) != 0 {
		t.Fatalf("fixed draft = %+v, %v", d, err)
	}
	if _, err := f.svc.Publish(ctx, f.admin, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdateDefinition(ctx, f.admin, d.ID, []byte(doc)); !errors.Is(err, workflow.ErrConflict) {
		t.Fatalf("edited a published version: %v", err)
	}
	next, err := f.svc.CreateDefinition(ctx, f.admin, []byte(doc))
	if err != nil || next.Version != 2 {
		t.Fatalf("next version = %+v, %v", next, err)
	}
	visible, _ := f.svc.ListDefinitions(ctx, f.alice)
	if len(visible) != 1 || visible[0].Version != 1 {
		t.Fatalf("a user sees %+v", visible)
	}
}

func TestAFailingActionFailsTheInstance(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	def := f.published(t, doc)
	in, _ := f.svc.Start(ctx, f.alice, def.ID, &workflow.Target{Kind: "asset", ID: assetID})
	task := openTask(t, f, f.bob)
	f.assets.fail = errors.New("412 precondition failed")

	done, err := f.svc.CompleteTask(ctx, f.bob, task.ID, "approved", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	if done.Status != workflow.InstanceFailed || *done.FailureCode != "action_failed" || *done.FailureElement != "activate" {
		t.Fatalf("instance = %+v", done)
	}
	if !strings.Contains(*done.FailureDetail, "412") {
		t.Fatalf("detail = %v", *done.FailureDetail)
	}
	detail, _ := f.svc.GetInstance(ctx, f.alice, in.ID)
	if detail.Status != workflow.InstanceFailed {
		t.Fatalf("stored status = %s", detail.Status)
	}
}

func TestAnOverdueReviewEscalates(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.domains.set(
		domain.RoleAssignment{SubjectType: domain.SubjectUser, SubjectID: f.bobID, Role: domain.RoleSteward},
		domain.RoleAssignment{SubjectType: domain.SubjectUser, SubjectID: f.carolID, Role: domain.RoleDomainAdmin},
	)
	def := f.published(t, doc)
	if _, err := f.svc.Start(ctx, f.alice, def.ID, &workflow.Target{Kind: "asset", ID: assetID}); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.RunTimers(ctx); err != nil {
		t.Fatal(err)
	}
	openTask(t, f, f.bob)

	*f.now = f.now.Add(49 * time.Hour)
	if err := f.svc.RunTimers(ctx); err != nil {
		t.Fatal(err)
	}

	if tasks, _ := f.svc.MyTasks(ctx, f.bob); len(tasks) != 0 {
		t.Fatalf("bob still has %+v", tasks)
	}
	escalated := openTask(t, f, f.carol)
	if escalated.NodeID != "escalated" {
		t.Fatalf("task = %+v", escalated)
	}
	if got := f.notes.types(f.carolID); len(got) != 1 || got[0] != workflow.TypeTaskEscalated {
		t.Fatalf("carol notified %v", got)
	}
	if err := f.svc.RunTimers(ctx); err != nil {
		t.Fatal(err)
	}
	if tasks, _ := f.svc.MyTasks(ctx, f.carol); len(tasks) != 1 {
		t.Fatalf("a timer fired twice: %+v", tasks)
	}
}

func TestOnlyParticipantsSeeAnInstance(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	def := f.published(t, doc)
	in, _ := f.svc.Start(ctx, f.alice, def.ID, &workflow.Target{Kind: "asset", ID: assetID})

	if _, err := f.svc.GetInstance(ctx, f.carol, in.ID); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("carol read it: %v", err)
	}
	for _, p := range []auth.Principal{f.alice, f.bob, f.admin} {
		if _, err := f.svc.GetInstance(ctx, p, in.ID); err != nil {
			t.Fatalf("%s: %v", p.DisplayName(), err)
		}
	}
	mine, _ := f.svc.ListInstances(ctx, f.bob, workflow.InstanceFilter{}, false)
	theirs, _ := f.svc.ListInstances(ctx, f.carol, workflow.InstanceFilter{}, false)
	if len(mine) != 1 || len(theirs) != 0 {
		t.Fatalf("bob %d, carol %d", len(mine), len(theirs))
	}
	if _, err := f.svc.ListInstances(ctx, f.bob, workflow.InstanceFilter{}, true); !errors.Is(err, workflow.ErrForbidden) {
		t.Fatalf("bob listed all: %v", err)
	}
}

func TestCancelClosesOpenTasks(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	def := f.published(t, doc)
	in, _ := f.svc.Start(ctx, f.alice, def.ID, &workflow.Target{Kind: "asset", ID: assetID})

	if _, err := f.svc.Cancel(ctx, f.alice, in.ID); !errors.Is(err, workflow.ErrForbidden) {
		t.Fatalf("alice cancelled: %v", err)
	}
	cancelled, err := f.svc.Cancel(ctx, f.admin, in.ID)
	if err != nil || cancelled.Status != workflow.InstanceCancelled {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	if tasks, _ := f.svc.MyTasks(ctx, f.bob); len(tasks) != 0 {
		t.Fatalf("bob still has %+v", tasks)
	}
}

func TestStartRejectsAMissingTarget(t *testing.T) {
	f := setup(t)
	def := f.published(t, doc)
	_, err := f.svc.Start(context.Background(), f.alice, def.ID, &workflow.Target{Kind: "asset", ID: "33333333-3333-4333-8333-333333333333"})
	if !errors.Is(err, workflow.ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
}

const formDoc = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:camunda="http://camunda.org/schema/1.0/bpmn" xmlns:dgu="https://dgu-development.github.io/schema/workflows" id="d">
<bpmn:process id="fill_fields" name="Fill">
<bpmn:startEvent id="start"/>
<bpmn:userTask id="review" name="Review" camunda:candidateGroups="role:steward" dgu:formFields="classification,lifecycle"/>
<bpmn:serviceTask id="untag" dgu:action="remove_tag" dgu:tag="draft"/>
<bpmn:serviceTask id="clear" dgu:action="clear_field" dgu:field="next_review"/>
<bpmn:endEvent id="ok"/>
<bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
<bpmn:sequenceFlow id="f2" sourceRef="review" targetRef="untag"/>
<bpmn:sequenceFlow id="f3" sourceRef="untag" targetRef="clear"/>
<bpmn:sequenceFlow id="f4" sourceRef="clear" targetRef="ok"/>
</bpmn:process></bpmn:definitions>`

func TestFormFieldsAndNewActions(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	def := f.published(t, formDoc)
	if _, err := f.svc.Start(ctx, f.alice, def.ID, &workflow.Target{Kind: "asset", ID: assetID}); err != nil {
		t.Fatal(err)
	}
	tasks, err := f.svc.MyTasks(ctx, f.bob)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks = %+v, %v", tasks, err)
	}
	if got := strings.Join(tasks[0].FormFields, ","); got != "classification,lifecycle" {
		t.Fatalf("form fields = %v", tasks[0].FormFields)
	}
	if _, err := f.svc.CompleteTask(ctx, f.bob, tasks[0].ID, "", "", nil); !errors.Is(err, workflow.ErrInvalidInput) {
		t.Fatalf("missing fields: %v", err)
	}
	done, err := f.svc.CompleteTask(ctx, f.bob, tasks[0].ID, "", "", map[string]any{
		"classification": "public",
		"lifecycle":      "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != workflow.InstanceCompleted {
		t.Fatalf("status = %s detail=%v", done.Status, done.FailureDetail)
	}
	if f.assets.patched["classification"] != "public" || f.assets.patched["lifecycle"] != "active" {
		t.Fatalf("patched = %+v", f.assets.patched)
	}
	if f.assets.removedTag != "draft" {
		t.Fatalf("removed tag = %q", f.assets.removedTag)
	}
	if v, ok := f.assets.patched["next_review"]; !ok || v != nil {
		t.Fatalf("clear_field next_review = %v ok=%v", v, ok)
	}
}

type fakeQuery struct {
	ids   []string
	total int
	err   error
}

func (f *fakeQuery) Match(context.Context, string, int) ([]string, int, error) {
	return f.ids, f.total, f.err
}

func TestStartQueryStartsOneRunPerMatch(t *testing.T) {
	f := setup(t)
	f.svc.WithQueries(&fakeQuery{ids: []string{assetID}, total: 3})
	def := f.published(t, doc)
	batch, err := f.svc.StartQuery(context.Background(), f.alice, def.ID, `tag = "pii"`, 10)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Started != 1 || batch.Total != 3 || len(batch.Instances) != 1 {
		t.Fatalf("batch = %+v", batch)
	}
	if batch.Instances[0].TargetID == nil || *batch.Instances[0].TargetID != assetID {
		t.Fatalf("target = %+v", batch.Instances[0].TargetID)
	}
}

func TestStartQueryRequiresMatcher(t *testing.T) {
	f := setup(t)
	def := f.published(t, doc)
	_, err := f.svc.StartQuery(context.Background(), f.alice, def.ID, `tag = "pii"`, 10)
	if !errors.Is(err, workflow.ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
}

const waitDoc = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:camunda="http://camunda.org/schema/1.0/bpmn" xmlns:dgu="https://dgu-development.github.io/schema/workflows" id="d">
<bpmn:process id="recheck" name="Recheck">
<bpmn:startEvent id="start"/>
<bpmn:intermediateCatchEvent id="pause"><bpmn:timerEventDefinition><bpmn:timeDuration>P7D</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>
<bpmn:serviceTask id="tell" dgu:action="notify" dgu:message="Review again" dgu:to="initiator,role:steward"/>
<bpmn:userTask id="again" name="Review again" camunda:candidateGroups="role:steward"/>
<bpmn:endEvent id="ok"/>
<bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="pause"/>
<bpmn:sequenceFlow id="f2" sourceRef="pause" targetRef="tell"/>
<bpmn:sequenceFlow id="f3" sourceRef="tell" targetRef="again"/>
<bpmn:sequenceFlow id="f4" sourceRef="again" targetRef="ok"/>
</bpmn:process></bpmn:definitions>`

func TestAWaitPausesTheRunAndThenNotifiesAndOpensTheNextTask(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	def := f.published(t, waitDoc)
	in, err := f.svc.Start(ctx, f.alice, def.ID, &workflow.Target{Kind: "asset", ID: assetID})
	if err != nil {
		t.Fatal(err)
	}

	detail, err := f.svc.GetInstance(ctx, f.alice, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Tasks) != 1 || !detail.Tasks[0].Wait || detail.Tasks[0].DueAt == nil ||
		!detail.Tasks[0].DueAt.Equal(f.now.Add(7*24*time.Hour)) || len(detail.Active) != 1 || detail.Active[0] != "pause" {
		t.Fatalf("detail = %+v active = %v", detail.Tasks, detail.Active)
	}
	if tasks, _ := f.svc.MyTasks(ctx, f.bob); len(tasks) != 0 {
		t.Fatalf("a wait is not an inbox task: %+v", tasks)
	}
	if _, err := f.svc.CompleteTask(ctx, f.admin, detail.Tasks[0].ID, "approved", "", nil); !errors.Is(err, workflow.ErrInvalidInput) {
		t.Fatalf("an admin decided a wait: %v", err)
	}

	*f.now = f.now.Add(6 * 24 * time.Hour)
	if err := f.svc.RunTimers(ctx); err != nil {
		t.Fatal(err)
	}
	if tasks, _ := f.svc.MyTasks(ctx, f.bob); len(tasks) != 0 {
		t.Fatalf("the wait ended early: %+v", tasks)
	}

	*f.now = f.now.Add(2 * 24 * time.Hour)
	if err := f.svc.RunTimers(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.notes.types(f.bobID); strings.Join(got, ",") != workflow.TypeWorkflowMessage+","+workflow.TypeTaskAssigned {
		t.Fatalf("bob was notified %v", got)
	}
	if got := f.notes.types(f.aliceID); strings.Join(got, ",") != workflow.TypeWorkflowMessage {
		t.Fatalf("the initiator was notified %v", got)
	}
	next := openTask(t, f, f.bob)
	if next.NodeID != "again" {
		t.Fatalf("task = %+v", next)
	}
	detail, _ = f.svc.GetInstance(ctx, f.alice, in.ID)
	if detail.Status != workflow.InstanceRunning {
		t.Fatalf("status = %s", detail.Status)
	}
	for _, task := range detail.Tasks {
		if task.Wait && task.Status != workflow.TaskCompleted {
			t.Fatalf("the wait is %s", task.Status)
		}
	}
}

func TestCancellingARunEndsItsWait(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	def := f.published(t, waitDoc)
	in, _ := f.svc.Start(ctx, f.alice, def.ID, &workflow.Target{Kind: "asset", ID: assetID})
	if _, err := f.svc.Cancel(ctx, f.admin, in.ID); err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(30 * 24 * time.Hour)
	if err := f.svc.RunTimers(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.notes.types(f.bobID); len(got) != 0 {
		t.Fatalf("a cancelled run kept going: %v", got)
	}
}

func TestABatchSkipsWhatItCannotStartAndReportsIt(t *testing.T) {
	f := setup(t)
	f.svc.WithQueries(&fakeQuery{ids: []string{"33333333-3333-4333-8333-333333333333", assetID}, total: 2})
	def := f.published(t, doc)
	batch, err := f.svc.StartQuery(context.Background(), f.alice, def.ID, `tag = "pii"`, 10)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Started != 1 || len(batch.Failed) != 1 || batch.Failed[0].Code != "invalid_input" {
		t.Fatalf("batch = %+v", batch)
	}
}

func TestABatchStopsWhenTheDefinitionIsNotRunnable(t *testing.T) {
	f := setup(t)
	f.svc.WithQueries(&fakeQuery{ids: []string{assetID}, total: 1})
	d, err := f.svc.CreateDefinition(context.Background(), f.admin, []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.StartQuery(context.Background(), f.alice, d.ID, `tag = "pii"`, 10); !errors.Is(err, workflow.ErrNotRunnable) {
		t.Fatalf("err = %v", err)
	}
}

func TestCapabilitiesListTheCatalogueAndTheBatchFeatureOnlyWithAMatcher(t *testing.T) {
	f := setup(t)
	caps := f.svc.Capabilities()
	if len(caps.Actions) != len(workflow.Actions) || slices.Contains(caps.Features, "batch_start") || !slices.Contains(caps.Features, "wait_timer") {
		t.Fatalf("caps = %+v", caps)
	}
	f.svc.WithQueries(&fakeQuery{})
	if !slices.Contains(f.svc.Capabilities().Features, "batch_start") {
		t.Fatal("batch_start missing with a matcher")
	}
}
