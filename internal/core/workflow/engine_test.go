package workflow

import (
	"context"
	"errors"
	"slices"
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
		"message start":     {`<bpmn:startEvent id="s"><bpmn:messageEventDefinition/></bpmn:startEvent><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="e"/>`, "unsupported_event"},
		"timer start empty": {`<bpmn:startEvent id="s"><bpmn:timerEventDefinition/></bpmn:startEvent><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="e"/>`, "timer_needs_cycle"},
		"no assignee":       {`<bpmn:startEvent id="s"/><bpmn:userTask id="u"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="u"/><bpmn:sequenceFlow id="b" sourceRef="u" targetRef="e"/>`, "task_needs_assignment"},
		"bad group":         {`<bpmn:startEvent id="s"/><bpmn:userTask id="u" camunda:candidateGroups="role:king"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="u"/><bpmn:sequenceFlow id="b" sourceRef="u" targetRef="e"/>`, "invalid_candidate_group"},
		"no action":         {`<bpmn:startEvent id="s"/><bpmn:serviceTask id="x"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="x"/><bpmn:sequenceFlow id="b" sourceRef="x" targetRef="e"/>`, "service_needs_action"},
		"code action":       {`<bpmn:startEvent id="s"/><bpmn:serviceTask id="x" dgu:action="exec"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="x"/><bpmn:sequenceFlow id="b" sourceRef="x" targetRef="e"/>`, "unknown_action"},
		"bad form field":    {`<bpmn:startEvent id="s"/><bpmn:userTask id="u" camunda:assignee="bob" dgu:formFields="bad-id!"/><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="u"/><bpmn:sequenceFlow id="b" sourceRef="u" targetRef="e"/>`, "invalid_form_field"},
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

func TestParseAcceptsFormFieldsAndNewActions(t *testing.T) {
	body := `
<bpmn:startEvent id="start"/>
<bpmn:userTask id="review" camunda:assignee="bob" dgu:formFields="classification,lifecycle"/>
<bpmn:serviceTask id="untag" dgu:action="remove_tag" dgu:tag="draft"/>
<bpmn:serviceTask id="clear" dgu:action="clear_field" dgu:field="next_review"/>
<bpmn:endEvent id="ok"/>
<bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
<bpmn:sequenceFlow id="f2" sourceRef="review" targetRef="untag"/>
<bpmn:sequenceFlow id="f3" sourceRef="untag" targetRef="clear"/>
<bpmn:sequenceFlow id="f4" sourceRef="clear" targetRef="ok"/>`
	p := mustParse(t, body)
	if got := strings.Join(p.Nodes["review"].FormFields, ","); got != "classification,lifecycle" {
		t.Fatalf("form fields = %v", p.Nodes["review"].FormFields)
	}
	if p.Nodes["untag"].Action != ActionRemoveTag || p.Nodes["clear"].Action != ActionClearField {
		t.Fatalf("actions = %s / %s", p.Nodes["untag"].Action, p.Nodes["clear"].Action)
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

func TestGroups(t *testing.T) {
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

const waiting = `
<bpmn:startEvent id="start"/>
<bpmn:intermediateCatchEvent id="pause"><bpmn:timerEventDefinition><bpmn:timeDuration>PT2H</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>
<bpmn:serviceTask id="tell" dgu:action="notify" dgu:message="Time is up"/>
<bpmn:endEvent id="ok"/>
<bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="pause"/>
<bpmn:sequenceFlow id="f2" sourceRef="pause" targetRef="tell"/>
<bpmn:sequenceFlow id="f3" sourceRef="tell" targetRef="ok"/>`

func TestAWaitPausesTheRunUntilItsTimerFires(t *testing.T) {
	now := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	exec := &recorder{}
	r := &Runner{Process: mustParse(t, waiting), Executor: exec, Now: func() time.Time { return now }}
	st := NewState(nil)

	step := r.Start(context.Background(), st)
	if step.Done || len(step.NewTasks) != 1 || step.NewTasks[0].Node.ID != "pause" {
		t.Fatalf("start = %+v", step)
	}
	wait := step.NewTasks[0]
	if wait.DueAt == nil || !wait.DueAt.Equal(now.Add(2*time.Hour)) || wait.Timer != "pause" {
		t.Fatalf("wait = %+v", wait)
	}
	if len(exec.ran) != 0 {
		t.Fatalf("ran before the timer: %v", exec.ran)
	}

	step, err := r.FireTimer(context.Background(), st, wait.TokenID, "pause")
	if err != nil {
		t.Fatal(err)
	}
	if !step.Done || strings.Join(exec.ran, ",") != "tell" {
		t.Fatalf("fire = %+v, ran %v", step, exec.ran)
	}
	if len(step.ReleasedWaits) != 1 || step.ReleasedWaits[0] != wait.TokenID || len(step.CancelledTasks) != 0 {
		t.Fatalf("released = %v cancelled = %v", step.ReleasedWaits, step.CancelledTasks)
	}
	if len(st.Tokens) != 0 {
		t.Fatalf("tokens left: %+v", st.Tokens)
	}
}

func TestAWaitTimerCannotReleaseAnotherToken(t *testing.T) {
	r := &Runner{Process: mustParse(t, approval), Executor: &recorder{}}
	st := NewState(nil)
	step := r.Start(context.Background(), st)
	if _, err := r.FireTimer(context.Background(), st, step.NewTasks[0].TokenID, "review"); err == nil {
		t.Fatal("a user task is not a timer")
	}
}

func TestWaitValidation(t *testing.T) {
	cases := map[string]string{
		"timer_needs_duration":    `<bpmn:startEvent id="s"/><bpmn:intermediateCatchEvent id="w"><bpmn:timerEventDefinition/></bpmn:intermediateCatchEvent><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="w"/><bpmn:sequenceFlow id="b" sourceRef="w" targetRef="e"/>`,
		"invalid_duration":        `<bpmn:startEvent id="s"/><bpmn:intermediateCatchEvent id="w"><bpmn:timerEventDefinition><bpmn:timeDuration>soon</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="w"/><bpmn:sequenceFlow id="b" sourceRef="w" targetRef="e"/>`,
		"wait_needs_one_outgoing": `<bpmn:startEvent id="s"/><bpmn:intermediateCatchEvent id="w"><bpmn:timerEventDefinition><bpmn:timeDuration>PT1H</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="w"/>`,
		"unsupported_event":       `<bpmn:startEvent id="s"/><bpmn:intermediateCatchEvent id="w"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent><bpmn:endEvent id="e"/><bpmn:sequenceFlow id="a" sourceRef="s" targetRef="w"/><bpmn:sequenceFlow id="b" sourceRef="w" targetRef="e"/>`,
	}
	for want, body := range cases {
		_, err := Parse(diagram(body))
		if !slices.Contains(codes(err), want) {
			t.Errorf("%s: got %v", want, codes(err))
		}
	}
}

func TestNotifyRecipientsAreValidated(t *testing.T) {
	for _, to := range []string{"", "initiator", "participants", "initiator, team:Data office", "role:steward,role:domain_admin@abc"} {
		if code := validateAction(&Node{Action: ActionNotify, Args: map[string]string{"message": "hi", "to": to}}); code != "" {
			t.Errorf("%q rejected: %s", to, code)
		}
	}
	for _, to := range []string{"everyone", "initiator,role:owner", "user:bob"} {
		if code := validateAction(&Node{Action: ActionNotify, Args: map[string]string{"message": "hi", "to": to}}); code != "invalid_recipients" {
			t.Errorf("%q accepted: %q", to, code)
		}
	}
}

func TestEveryCataloguedActionValidates(t *testing.T) {
	sample := map[string]string{"message": "hi", "field": "lifecycle", "value": "active", "tag": "pii", "term": "Customer"}
	for _, spec := range Actions {
		args := map[string]string{}
		for _, arg := range spec.Args {
			if arg.Required {
				args[arg.Name] = sample[arg.Name]
			}
		}
		if code := validateAction(&Node{Action: spec.ID, Args: args}); code != "" {
			t.Errorf("%s: %s", spec.ID, code)
		}
		for _, arg := range spec.Args {
			if !arg.Required {
				continue
			}
			short := map[string]string{}
			for k, v := range args {
				if k != arg.Name {
					short[k] = v
				}
			}
			if code := validateAction(&Node{Action: spec.ID, Args: short}); code == "" {
				t.Errorf("%s runs without its required %s", spec.ID, arg.Name)
			}
		}
	}
}

const remindable = `
<bpmn:startEvent id="start"/>
<bpmn:userTask id="review" name="Review" camunda:candidateGroups="role:steward"/>
<bpmn:boundaryEvent id="late" attachedToRef="review" dgu:remind="%s"><bpmn:timerEventDefinition><bpmn:timeDuration>P2D</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:boundaryEvent>
<bpmn:endEvent id="ok"/><bpmn:endEvent id="late_end"/>
<bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
<bpmn:sequenceFlow id="f2" sourceRef="review" targetRef="ok"/>
<bpmn:sequenceFlow id="f3" sourceRef="late" targetRef="late_end"/>`

func TestAReminderIsScheduledBeforeTheTimerAndValidated(t *testing.T) {
	now := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	p, err := Parse(diagram(strings.Replace(remindable, "%s", "PT12H", 1)))
	if err != nil {
		t.Fatal(err)
	}
	step := (&Runner{Process: p, Executor: &recorder{}, Now: func() time.Time { return now }}).Start(context.Background(), NewState(nil))
	task := step.NewTasks[0]
	if task.RemindAt == nil || !task.RemindAt.Equal(now.Add(36*time.Hour)) || !task.DueAt.Equal(now.Add(48*time.Hour)) {
		t.Fatalf("task = %+v", task)
	}
	for _, bad := range []string{"PT48H", "P3D", "soon"} {
		_, err := Parse(diagram(strings.Replace(remindable, "%s", bad, 1)))
		if !slices.Contains(codes(err), "invalid_reminder") {
			t.Errorf("%q: %v", bad, codes(err))
		}
	}
}

func startTimer(cycle, attrs string) string {
	return `<bpmn:startEvent id="start" ` + attrs + `><bpmn:timerEventDefinition><bpmn:timeCycle>` + cycle + `</bpmn:timeCycle></bpmn:timerEventDefinition></bpmn:startEvent>
<bpmn:serviceTask id="tell" dgu:action="notify" dgu:message="Recertify"/><bpmn:endEvent id="ok"/>
<bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="tell"/><bpmn:sequenceFlow id="f2" sourceRef="tell" targetRef="ok"/>`
}

func TestATimerStartEventCarriesItsCycleAndQuery(t *testing.T) {
	p, err := Parse(diagram(startTimer("0 8 * * 1", `dgu:query="tag = &quot;pii&quot;"`)))
	if err != nil {
		t.Fatal(err)
	}
	if p.Schedule == nil || p.Schedule.Cycle != "0 8 * * 1" || p.Schedule.Query != `tag = "pii"` {
		t.Fatalf("schedule = %+v", p.Schedule)
	}
	plain := mustParse(t, approval)
	if plain.Schedule != nil {
		t.Fatalf("a manual start has a schedule: %+v", plain.Schedule)
	}
}

func TestTimerStartCyclesAreValidated(t *testing.T) {
	for cycle, want := range map[string]string{
		"* * * * *":   "cycle_too_frequent",
		"*/5 * * * *": "cycle_too_frequent",
		"PT5M":        "cycle_too_frequent",
		"soon":        "invalid_cycle",
		"1 2 3":       "invalid_cycle",
		"":            "timer_needs_cycle",
	} {
		_, err := Parse(diagram(startTimer(cycle, "")))
		if !slices.Contains(codes(err), want) {
			t.Errorf("%q: got %v, want %s", cycle, codes(err), want)
		}
	}
	for _, ok := range []string{"0 8 * * 1", "*/15 * * * *", "PT6H", "P1D", "P1W"} {
		if _, err := Parse(diagram(startTimer(ok, ""))); err != nil {
			t.Errorf("%q rejected: %v", ok, codes(err))
		}
	}
	if _, err := Parse(diagram(strings.Replace(startTimer("PT6H", ""), "timerEventDefinition", "messageEventDefinition", 2))); !slices.Contains(codes(err), "unsupported_event") {
		t.Errorf("a message start is not supported: %v", codes(err))
	}
}

func TestACycleSaysWhenItFiresNext(t *testing.T) {
	from := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	cron, _ := ParseCycle("0 8 * * *")
	if got := cron.Next(from); !got.Equal(time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("cron next = %v", got)
	}
	every, _ := ParseCycle("PT6H")
	if got := every.Next(from); !got.Equal(from.Add(6 * time.Hour)) {
		t.Fatalf("interval next = %v", got)
	}
}
