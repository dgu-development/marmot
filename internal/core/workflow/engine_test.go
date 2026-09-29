package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const header = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:camunda="http://camunda.org/schema/1.0/bpmn" xmlns:dgu="https://dgu-development.github.io/schema/workflows" id="d">
<bpmn:process id="p" name="Approval" isExecutable="true">`

const footer = `</bpmn:process></bpmn:definitions>`

func diagram(body string) []byte { return []byte(header + body + footer) }

const approval = `
<bpmn:startEvent id="start"/>
<bpmn:userTask id="review" name="Review" camunda:candidateGroups="role:steward"/>
<bpmn:exclusiveGateway id="gw" default="f_no"/>
<bpmn:serviceTask id="activate" dgu:action="set_field" dgu:field="lifecycle" dgu:value="active"/>
<bpmn:endEvent id="ok"/>
<bpmn:endEvent id="ko"/>
<bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
<bpmn:sequenceFlow id="f2" sourceRef="review" targetRef="gw"/>
<bpmn:sequenceFlow id="f_yes" sourceRef="gw" targetRef="activate"><bpmn:conditionExpression>${decision == "approved"}</bpmn:conditionExpression></bpmn:sequenceFlow>
<bpmn:sequenceFlow id="f_no" sourceRef="gw" targetRef="ko"/>
<bpmn:sequenceFlow id="f3" sourceRef="activate" targetRef="ok"/>`

type recorder struct {
	ran  []string
	fail error
}

func (r *recorder) Execute(_ context.Context, n *Node, _ map[string]string) error {
	r.ran = append(r.ran, n.ID)
	return r.fail
}

func mustParse(t *testing.T, body string) *Process {
	t.Helper()
	p, err := Parse(diagram(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return p
}

func codes(err error) []string {
	var v *ValidationError
	if !errors.As(err, &v) {
		return nil
	}
	out := make([]string, 0, len(v.Issues))
	for _, i := range v.Issues {
		out = append(out, i.Code)
	}
	return out
}

func TestParseReadsAssignmentsActionsAndConditions(t *testing.T) {
	p := mustParse(t, approval)
	if p.Start != "start" || p.Name != "Approval" {
		t.Fatalf("process = %+v", p)
	}
	if got := p.Nodes["review"].CandidateGroups; len(got) != 1 || got[0] != "role:steward" {
		t.Fatalf("candidate groups = %v", got)
	}
	activate := p.Nodes["activate"]
	if activate.Action != ActionSetField || activate.Args["field"] != "lifecycle" || activate.Args["value"] != "active" {
		t.Fatalf("service task = %+v", activate)
	}
	if p.Flows["f_yes"].Condition != `${decision == "approved"}` || p.Nodes["gw"].Default != "f_no" {
		t.Fatalf("gateway = %+v", p.Nodes["gw"])
	}
}

func TestParseRejectsWhatWouldNotRun(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"script":            {`<bpmn:startEvent id="s"/><bpmn:scriptTask id="x"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="e"/>`, "script_not_allowed"},
		"subprocess":        {`<bpmn:startEvent id="s"/><bpmn:subProcess id="x"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="e"/>`, "unsupported_element"},
		"timer start":       {`<bpmn:startEvent id="s"><bpmn:timerEventDefinition/></bpmn:startEvent><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="e"/>`, "unsupported_event"},
		"no assignee":       {`<bpmn:startEvent id="s"/><bpmn:userTask id="u"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="u"/><bpmn:sequenceFlow id="b" sourceRef="u" targetRef="e"/>`, "task_needs_assignment"},
		"bad group":         {`<bpmn:startEvent id="s"/><bpmn:userTask id="u" camunda:candidateGroups="role:king"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="u"/><bpmn:sequenceFlow id="b" sourceRef="u" targetRef="e"/>`, "invalid_candidate_group"},
		"no action":         {`<bpmn:startEvent id="s"/><bpmn:serviceTask id="x"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="x"/><bpmn:sequenceFlow id="b" sourceRef="x" targetRef="e"/>`, "service_needs_action"},
		"code action":       {`<bpmn:startEvent id="s"/><bpmn:serviceTask id="x" dgu:action="exec"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="x"/><bpmn:sequenceFlow id="b" sourceRef="x" targetRef="e"/>`, "unknown_action"},
		"no end":            {`<bpmn:startEvent id="s"/>`, "no_end"},
		"two starts":        {`<bpmn:startEvent id="s"/><bpmn:startEvent id="t"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="e"/><bpmn:sequenceFlow id="b" sourceRef="t" targetRef="e"/>`, "several_starts"},
		"dangling":          {`<bpmn:startEvent id="s"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="nowhere"/>`, "flow_dangling"},
		"no condition":      {`<bpmn:startEvent id="s"/><bpmn:exclusiveGateway id="g"/><bpmn:endEvent id="e"/><bpmn:endEvent id="e2"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="g"/><bpmn:sequenceFlow id="b" sourceRef="g" targetRef="e"/><bpmn:sequenceFlow id="c" sourceRef="g" targetRef="e2"/>`, "branch_needs_condition"},
		"code in condition": {`<bpmn:startEvent id="s"/><bpmn:exclusiveGateway id="g" default="c"/><bpmn:endEvent id="e"/><bpmn:endEvent id="e2"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="g"/><bpmn:sequenceFlow id="b" sourceRef="g" targetRef="e"><bpmn:conditionExpression>${execution.getVariable("x")}</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="c" sourceRef="g" targetRef="e2"/>`, "invalid_condition"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(diagram(tc.body))
			if !errors.Is(err, ErrInvalidDiagram) {
				t.Fatalf("err = %v, want invalid diagram", err)
			}
			got := codes(err)
			for _, c := range got {
				if c == tc.want {
					return
				}
			}
			t.Fatalf("codes = %v, want %s", got, tc.want)
		})
	}
}

func TestParseRejectsDocumentsThatAreNotOneBPMNProcess(t *testing.T) {
	for name, doc := range map[string]string{
		"malformed":  "<bpmn:definitions",
		"not bpmn":   `<definitions xmlns="urn:x"/>`,
		"doctype":    `<?xml version="1.0"?><!DOCTYPE x [<!ENTITY a "b">]><bpmn:definitions xmlns:bpmn="` + NamespaceBPMN + `"/>`,
		"no process": `<bpmn:definitions xmlns:bpmn="` + NamespaceBPMN + `"/>`,
		"two":        `<bpmn:definitions xmlns:bpmn="` + NamespaceBPMN + `"><bpmn:process id="a"/><bpmn:process id="b"/></bpmn:definitions>`,
	} {
		if _, err := Parse([]byte(doc)); !errors.Is(err, ErrInvalidDiagram) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := Parse([]byte(strings.Repeat(" ", MaxDiagramBytes+1))); !errors.Is(err, ErrInvalidDiagram) {
		t.Errorf("oversized diagram accepted: %v", err)
	}
}

func TestConditions(t *testing.T) {
	c, err := ParseCondition(`${decision == 'approved' && level != 2}`)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Eval(map[string]string{"decision": "approved", "level": "1"}) {
		t.Fatal("expected true")
	}
	if c.Eval(map[string]string{"decision": "approved", "level": "2"}) || c.Eval(nil) {
		t.Fatal("expected false")
	}
	for _, bad := range []string{"", "a || b", "a = 1", "a == 'x", "a == foo()", "a.b() == 1"} {
		if _, err := ParseCondition(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDurations(t *testing.T) {
	for text, want := range map[string]time.Duration{"PT4H": 4 * time.Hour, "P2D": 48 * time.Hour, "P1W": 7 * 24 * time.Hour, "P1DT30M": 24*time.Hour + 30*time.Minute} {
		got, err := ParseDuration(text)
		if err != nil || got != want {
			t.Errorf("%s = %v, %v", text, got, err)
		}
	}
	for _, bad := range []string{"P", "PT", "P1M", "P1Y", "3 days", "PT0S"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestGroupsAndFieldValues(t *testing.T) {
	g, err := ParseGroup("role:domain_admin@abc")
	if err != nil || g.Kind != GroupRole || g.Value != "domain_admin" || g.DomainID != "abc" {
		t.Fatalf("group = %+v, %v", g, err)
	}
	if g, _ := ParseGroup("team:Data office"); g.Kind != GroupTeam || g.Value != "Data office" {
		t.Fatalf("team = %+v", g)
	}
	for _, bad := range []string{"steward", "role:", "user:x", "role:owner"} {
		if _, err := ParseGroup(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if FieldValue("true") != true || FieldValue("3") != float64(3) || FieldValue("active") != "active" || FieldValue(`{"a":1}`) != `{"a":1}` {
		t.Fatal("field values")
	}
}

func TestRunnerApprovalTakesTheApprovedBranch(t *testing.T) {
	exec := &recorder{}
	r := &Runner{Process: mustParse(t, approval), Executor: exec}
	st := NewState(nil)

	step := r.Start(context.Background(), st)
	if len(step.NewTasks) != 1 || step.NewTasks[0].Node.ID != "review" || step.Done {
		t.Fatalf("start = %+v", step)
	}
	step, err := r.Complete(context.Background(), st, step.NewTasks[0].TokenID, map[string]string{"decision": "approved"})
	if err != nil {
		t.Fatal(err)
	}
	if !step.Done || step.Failure != nil || len(exec.ran) != 1 || exec.ran[0] != "activate" {
		t.Fatalf("complete = %+v, ran %v", step, exec.ran)
	}
}

func TestRunnerRejectionTakesTheDefaultBranch(t *testing.T) {
	exec := &recorder{}
	r := &Runner{Process: mustParse(t, approval), Executor: exec}
	st := NewState(nil)
	start := r.Start(context.Background(), st)

	step, _ := r.Complete(context.Background(), st, start.NewTasks[0].TokenID, map[string]string{"decision": "rejected"})

	if !step.Done || len(exec.ran) != 0 {
		t.Fatalf("step = %+v, ran %v", step, exec.ran)
	}
}

func TestRunnerFailsWhenAnActionFails(t *testing.T) {
	r := &Runner{Process: mustParse(t, approval), Executor: &recorder{fail: errors.New("412")}}
	st := NewState(nil)
	start := r.Start(context.Background(), st)

	step, _ := r.Complete(context.Background(), st, start.NewTasks[0].TokenID, map[string]string{"decision": "approved"})

	if step.Failure == nil || step.Failure.Code != "action_failed" || step.Failure.Element != "activate" || step.Done {
		t.Fatalf("step = %+v", step)
	}
}

const parallel = `
<bpmn:startEvent id="start"/>
<bpmn:parallelGateway id="fork"/>
<bpmn:userTask id="steward" camunda:candidateGroups="role:steward"/>
<bpmn:userTask id="owner" camunda:candidateGroups="role:domain_admin"/>
<bpmn:parallelGateway id="join"/>
<bpmn:endEvent id="end"/>
<bpmn:sequenceFlow id="a" sourceRef="start" targetRef="fork"/>
<bpmn:sequenceFlow id="b" sourceRef="fork" targetRef="steward"/>
<bpmn:sequenceFlow id="c" sourceRef="fork" targetRef="owner"/>
<bpmn:sequenceFlow id="d" sourceRef="steward" targetRef="join"/>
<bpmn:sequenceFlow id="e" sourceRef="owner" targetRef="join"/>
<bpmn:sequenceFlow id="f" sourceRef="join" targetRef="end"/>`

func TestRunnerParallelReviewsJoinBeforeEnding(t *testing.T) {
	r := &Runner{Process: mustParse(t, parallel)}
	st := NewState(nil)
	start := r.Start(context.Background(), st)
	if len(start.NewTasks) != 2 {
		t.Fatalf("tasks = %+v", start.NewTasks)
	}

	first, _ := r.Complete(context.Background(), st, start.NewTasks[0].TokenID, nil)
	if first.Done || first.Failure != nil {
		t.Fatalf("finished after one review: %+v", first)
	}
	second, _ := r.Complete(context.Background(), st, start.NewTasks[1].TokenID, nil)
	if !second.Done {
		t.Fatalf("not finished after both reviews: %+v", second)
	}
}

func TestRunnerTerminateCancelsOpenTasks(t *testing.T) {
	body := strings.Replace(parallel, `<bpmn:endEvent id="end"/>`, `<bpmn:endEvent id="end"/><bpmn:endEvent id="stop"><bpmn:terminateEventDefinition/></bpmn:endEvent>`, 1)
	body = strings.Replace(body, `<bpmn:sequenceFlow id="d" sourceRef="steward" targetRef="join"/>`, `<bpmn:sequenceFlow id="d" sourceRef="steward" targetRef="stop"/>`, 1)
	body = strings.Replace(body, `<bpmn:parallelGateway id="join"/>`, ``, 1)
	body = strings.Replace(body, `<bpmn:sequenceFlow id="e" sourceRef="owner" targetRef="join"/>`, `<bpmn:sequenceFlow id="e" sourceRef="owner" targetRef="end"/>`, 1)
	body = strings.Replace(body, `<bpmn:sequenceFlow id="f" sourceRef="join" targetRef="end"/>`, ``, 1)
	r := &Runner{Process: mustParse(t, body)}
	st := NewState(nil)
	start := r.Start(context.Background(), st)
	steward := start.NewTasks[0]
	if steward.Node.ID != "steward" {
		steward = start.NewTasks[1]
	}

	step, _ := r.Complete(context.Background(), st, steward.TokenID, nil)

	if !step.Done || len(step.CancelledTasks) != 1 || len(st.Tokens) != 0 {
		t.Fatalf("step = %+v, tokens %v", step, st.Tokens)
	}
}

const escalation = `
<bpmn:startEvent id="start"/>
<bpmn:userTask id="review" camunda:assignee="alice"/>
<bpmn:boundaryEvent id="late" attachedToRef="review"><bpmn:timerEventDefinition><bpmn:timeDuration>P2D</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:boundaryEvent>
<bpmn:userTask id="escalated" camunda:candidateGroups="role:domain_admin"/>
<bpmn:endEvent id="end"/>
<bpmn:sequenceFlow id="a" sourceRef="start" targetRef="review"/>
<bpmn:sequenceFlow id="b" sourceRef="review" targetRef="end"/>
<bpmn:sequenceFlow id="c" sourceRef="late" targetRef="escalated"/>
<bpmn:sequenceFlow id="d" sourceRef="escalated" targetRef="end"/>`

func TestRunnerTimerEscalatesAnOverdueTask(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	r := &Runner{Process: mustParse(t, escalation), Now: func() time.Time { return now }}
	st := NewState(nil)
	start := r.Start(context.Background(), st)
	task := start.NewTasks[0]
	if task.Timer != "late" || task.DueAt == nil || !task.DueAt.Equal(now.Add(48*time.Hour)) {
		t.Fatalf("task = %+v", task)
	}

	step, err := r.FireTimer(context.Background(), st, task.TokenID, "late")
	if err != nil {
		t.Fatal(err)
	}

	if len(step.CancelledTasks) != 1 || len(step.NewTasks) != 1 || step.NewTasks[0].Node.ID != "escalated" {
		t.Fatalf("step = %+v", step)
	}
	if _, err := r.Complete(context.Background(), st, task.TokenID, nil); err == nil {
		t.Fatal("the cancelled task could still be completed")
	}
}

func TestRunnerNonInterruptingTimerKeepsTheTask(t *testing.T) {
	body := strings.Replace(escalation, `attachedToRef="review"`, `attachedToRef="review" cancelActivity="false"`, 1)
	r := &Runner{Process: mustParse(t, body)}
	st := NewState(nil)
	start := r.Start(context.Background(), st)

	step, _ := r.FireTimer(context.Background(), st, start.NewTasks[0].TokenID, "late")

	if len(step.CancelledTasks) != 0 || len(st.Tokens) != 2 {
		t.Fatalf("step = %+v, tokens %v", step, st.Tokens)
	}
}

func TestRunnerStopsALoopWithoutWaits(t *testing.T) {
	body := `<bpmn:startEvent id="s"/><bpmn:exclusiveGateway id="g" default="back"/><bpmn:parallelGateway id="p"/><bpmn:endEvent id="e"/>
<bpmn:sequenceFlow id="a" sourceRef="s" targetRef="g"/>
<bpmn:sequenceFlow id="back" sourceRef="g" targetRef="p"/>
<bpmn:sequenceFlow id="out" sourceRef="g" targetRef="e"><bpmn:conditionExpression>x == 1</bpmn:conditionExpression></bpmn:sequenceFlow>
<bpmn:sequenceFlow id="loop" sourceRef="p" targetRef="g"/>`
	r := &Runner{Process: mustParse(t, body)}

	step := r.Start(context.Background(), NewState(nil))

	if step.Failure == nil || step.Failure.Code != "loop_limit" {
		t.Fatalf("step = %+v", step)
	}
}
