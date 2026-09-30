// Package workflow runs governance workflows modelled in BPMN 2.0. It executes
// a declared subset of the standard and never runs code carried by a diagram:
// service tasks pick an action the platform implements, and gateway
// conditions are plain comparisons on instance variables.
package workflow

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	NamespaceBPMN    = "http://www.omg.org/spec/BPMN/20100524/MODEL"
	NamespaceCamunda = "http://camunda.org/schema/1.0/bpmn"
	// NamespaceDGU carries the platform's own attributes (dgu:action, …), the
	// way Camunda uses its namespace, so the XML stays valid in other modelers.
	NamespaceDGU = "https://dgu-development.github.io/schema/workflows"

	// MaxDiagramBytes bounds an imported diagram.
	MaxDiagramBytes = 1 << 20
)

// NodeType is a BPMN flow node the engine knows about.
type NodeType string

const (
	NodeStart            NodeType = "startEvent"
	NodeEnd              NodeType = "endEvent"
	NodeUserTask         NodeType = "userTask"
	NodeServiceTask      NodeType = "serviceTask"
	NodeExclusiveGateway NodeType = "exclusiveGateway"
	NodeParallelGateway  NodeType = "parallelGateway"
	NodeBoundaryEvent    NodeType = "boundaryEvent"
	// NodeWait is an intermediate catch event with a timer: the run pauses
	// there for a duration and then goes on.
	NodeWait NodeType = "intermediateCatchEvent"
)

// Node is one flow node of the process.
type Node struct {
	ID       string   `json:"id"`
	Type     NodeType `json:"type"`
	Name     string   `json:"name,omitempty"`
	Incoming []string `json:"-"`
	Outgoing []string `json:"-"`

	// Terminate is set on an end event with a terminate definition.
	Terminate bool `json:"-"`
	// Default is the default flow of an exclusive gateway.
	Default string `json:"-"`

	// User task assignment (camunda:assignee, camunda:candidateUsers,
	// camunda:candidateGroups).
	Assignee        string   `json:"-"`
	CandidateUsers  []string `json:"-"`
	CandidateGroups []string `json:"-"`
	// FormFields are governed field ids the decider must supply (dgu:formFields).
	FormFields []string `json:"-"`

	// Service task action and its arguments (dgu:action, dgu:field, …).
	Action  string            `json:"-"`
	Args    map[string]string `json:"-"`
	Timeout string            `json:"-"`

	// Timer of a boundary event or a wait: for a boundary event, the task it
	// hangs on, its ISO 8601 duration and whether it cancels the task.
	AttachedTo     string `json:"-"`
	Duration       string `json:"-"`
	CancelActivity bool   `json:"-"`
	// Cycle and Query belong to a timer start event: when it fires (timeCycle)
	// and the asset query it starts one run per match for (dgu:query).
	Cycle string `json:"-"`
	Query string `json:"-"`
	// Remind is how long before a boundary timer fires the task's candidates are
	// reminded (dgu:remind, ISO 8601).
	Remind string `json:"-"`
}

// Flow is a sequence flow.
type Flow struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Name      string `json:"name,omitempty"`
	Condition string `json:"condition,omitempty"`
}

// Process is the executable view of a BPMN diagram.
type Process struct {
	ID    string
	Name  string
	Start string
	Nodes map[string]*Node
	Flows map[string]*Flow
	// Order keeps document order, for stable validation output.
	Order []string
	// Schedule is set when the start event is a timer: the process starts itself.
	Schedule *StartSchedule
}

// StartSchedule is the timer start event of a process.
type StartSchedule struct {
	Cycle string
	// Query is an asset query: each fire starts one run per match, up to the batch cap.
	// Empty starts a single run with no target.
	Query string
}

// Issue is a reason a diagram cannot run. Code is stable for clients to
// translate; Element is the BPMN id it refers to, when there is one.
type Issue struct {
	Element string `json:"element,omitempty"`
	Code    string `json:"code"`
	Detail  string `json:"detail,omitempty"`
}

// ErrInvalidDiagram wraps every parse or validation failure.
var ErrInvalidDiagram = errors.New("invalid workflow diagram")

// ValidationError lists every issue found, not only the first.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	codes := make([]string, 0, len(e.Issues))
	for _, i := range e.Issues {
		codes = append(codes, i.Code)
	}
	return fmt.Sprintf("invalid workflow diagram: %s", strings.Join(codes, ", "))
}

func (e *ValidationError) Unwrap() error { return ErrInvalidDiagram }

type element struct {
	name     xml.Name
	attrs    []xml.Attr
	children []*element
	text     strings.Builder
}

func (e *element) attr(space, local string) string {
	for _, a := range e.attrs {
		if a.Name.Local == local && (a.Name.Space == space || (space == "" && a.Name.Space == "")) {
			return a.Value
		}
	}
	return ""
}

func (e *element) hasAttr(space, local string) bool {
	for _, a := range e.attrs {
		if a.Name.Local == local && a.Name.Space == space {
			return true
		}
	}
	return false
}

func (e *element) bpmnChildren(local string) []*element {
	var out []*element
	for _, c := range e.children {
		if c.name.Space == NamespaceBPMN && c.name.Local == local {
			out = append(out, c)
		}
	}
	return out
}

func parseTree(data []byte) (*element, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	// Entities and external DTDs are never resolved: encoding/xml does not
	// fetch them, and Strict rejects undeclared entities.
	decoder.Strict = true
	var root *element
	var stack []*element
	for {
		tok, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			el := &element{name: t.Name, attrs: t.Attr}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("more than one root element")
				}
				root = el
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, el)
			}
			stack = append(stack, el)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(t)
			}
		case xml.Directive:
			return nil, errors.New("XML directives are not allowed")
		}
	}
	if root == nil {
		return nil, errors.New("empty document")
	}
	return root, nil
}

// Parse reads a BPMN 2.0 document and validates it against the executable
// subset. The returned error is a *ValidationError when the XML is well formed
// but does not run.
func Parse(data []byte) (*Process, error) {
	if len(data) > MaxDiagramBytes {
		return nil, &ValidationError{Issues: []Issue{{Code: "too_large"}}}
	}
	root, err := parseTree(data)
	if err != nil {
		return nil, &ValidationError{Issues: []Issue{{Code: "malformed_xml", Detail: err.Error()}}}
	}
	if root.name.Space != NamespaceBPMN || root.name.Local != "definitions" {
		return nil, &ValidationError{Issues: []Issue{{Code: "not_bpmn"}}}
	}
	processes := root.bpmnChildren("process")
	switch {
	case len(processes) == 0:
		return nil, &ValidationError{Issues: []Issue{{Code: "no_process"}}}
	case len(processes) > 1:
		return nil, &ValidationError{Issues: []Issue{{Code: "several_processes"}}}
	}
	p, issues := buildProcess(processes[0])
	issues = append(issues, validate(p)...)
	if len(issues) > 0 {
		return nil, &ValidationError{Issues: issues}
	}
	return p, nil
}

// Artifacts and lanes are presentation: accepted and ignored.
var ignored = map[string]bool{
	"laneSet": true, "textAnnotation": true, "association": true, "group": true,
	"documentation": true, "extensionElements": true, "dataObject": true,
	"dataObjectReference": true, "dataStoreReference": true, "incoming": true, "outgoing": true,
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func eventDefinitions(el *element) []*element {
	var out []*element
	for _, c := range el.children {
		if c.name.Space == NamespaceBPMN && strings.HasSuffix(c.name.Local, "EventDefinition") {
			out = append(out, c)
		}
	}
	return out
}

func buildProcess(el *element) (*Process, []Issue) {
	p := &Process{
		ID:    el.attr("", "id"),
		Name:  el.attr("", "name"),
		Nodes: map[string]*Node{},
		Flows: map[string]*Flow{},
	}
	var issues []Issue
	for _, c := range el.children {
		if c.name.Space != NamespaceBPMN {
			continue
		}
		id := c.attr("", "id")
		local := c.name.Local
		if ignored[local] {
			continue
		}
		if local == "sequenceFlow" {
			f := &Flow{ID: id, Source: c.attr("", "sourceRef"), Target: c.attr("", "targetRef"), Name: c.attr("", "name")}
			for _, cond := range c.bpmnChildren("conditionExpression") {
				f.Condition = strings.TrimSpace(cond.text.String())
			}
			p.Flows[id] = f
			continue
		}
		n := &Node{ID: id, Type: NodeType(local), Name: c.attr("", "name")}
		defs := eventDefinitions(c)
		switch n.Type {
		case NodeStart:
			for _, d := range defs {
				if d.name.Local != "timerEventDefinition" || n.Cycle != "" {
					issues = append(issues, Issue{Element: id, Code: "unsupported_event", Detail: d.name.Local})
					continue
				}
				for _, cycle := range d.bpmnChildren("timeCycle") {
					n.Cycle = strings.TrimSpace(cycle.text.String())
				}
				if n.Cycle == "" {
					issues = append(issues, Issue{Element: id, Code: "timer_needs_cycle"})
				}
			}
			n.Query = strings.TrimSpace(c.attr(NamespaceDGU, "query"))
		case NodeEnd:
			for _, d := range defs {
				if d.name.Local == "terminateEventDefinition" {
					n.Terminate = true
				} else {
					issues = append(issues, Issue{Element: id, Code: "unsupported_event", Detail: d.name.Local})
				}
			}
		case NodeUserTask:
			n.Assignee = strings.TrimSpace(c.attr(NamespaceCamunda, "assignee"))
			n.CandidateUsers = splitList(c.attr(NamespaceCamunda, "candidateUsers"))
			n.CandidateGroups = splitList(c.attr(NamespaceCamunda, "candidateGroups"))
			n.FormFields = splitList(c.attr(NamespaceDGU, "formFields"))
		case NodeServiceTask:
			n.Action = c.attr(NamespaceDGU, "action")
			n.Args = map[string]string{}
			for _, a := range c.attrs {
				if a.Name.Space == NamespaceDGU && a.Name.Local != "action" {
					n.Args[a.Name.Local] = a.Value
				}
			}
		case NodeExclusiveGateway:
			n.Default = c.attr("", "default")
		case NodeParallelGateway:
		case NodeWait:
			if len(defs) != 1 || defs[0].name.Local != "timerEventDefinition" {
				issues = append(issues, Issue{Element: id, Code: "unsupported_event"})
				break
			}
			for _, d := range defs[0].bpmnChildren("timeDuration") {
				n.Duration = strings.TrimSpace(d.text.String())
			}
			if n.Duration == "" {
				issues = append(issues, Issue{Element: id, Code: "timer_needs_duration"})
			}
		case NodeBoundaryEvent:
			n.AttachedTo = c.attr("", "attachedToRef")
			n.CancelActivity = !c.hasAttr("", "cancelActivity") || c.attr("", "cancelActivity") != "false"
			if len(defs) != 1 || defs[0].name.Local != "timerEventDefinition" {
				issues = append(issues, Issue{Element: id, Code: "unsupported_event"})
				break
			}
			for _, d := range defs[0].bpmnChildren("timeDuration") {
				n.Duration = strings.TrimSpace(d.text.String())
			}
			n.Remind = strings.TrimSpace(c.attr(NamespaceDGU, "remind"))
			if n.Duration == "" {
				issues = append(issues, Issue{Element: id, Code: "timer_needs_duration"})
			}
		case "scriptTask":
			issues = append(issues, Issue{Element: id, Code: "script_not_allowed"})
			continue
		default:
			issues = append(issues, Issue{Element: id, Code: "unsupported_element", Detail: local})
			continue
		}
		if id == "" {
			issues = append(issues, Issue{Code: "missing_id", Detail: local})
			continue
		}
		p.Nodes[id] = n
		p.Order = append(p.Order, id)
	}
	for _, f := range p.Flows {
		if src, ok := p.Nodes[f.Source]; ok {
			src.Outgoing = append(src.Outgoing, f.ID)
		}
		if dst, ok := p.Nodes[f.Target]; ok {
			dst.Incoming = append(dst.Incoming, f.ID)
		}
	}
	for _, n := range p.Nodes {
		sortByDocument(p, n.Outgoing)
		sortByDocument(p, n.Incoming)
	}
	return p, issues
}

func sortByDocument(p *Process, flows []string) {
	// Flow ids have no document order of their own; sorting keeps the engine
	// deterministic across runs.
	for i := 1; i < len(flows); i++ {
		for j := i; j > 0 && flows[j] < flows[j-1]; j-- {
			flows[j], flows[j-1] = flows[j-1], flows[j]
		}
	}
}

func validate(p *Process) []Issue {
	var issues []Issue
	var starts []string
	ends := 0
	for _, f := range sortedFlows(p) {
		if _, ok := p.Nodes[f.Source]; !ok {
			issues = append(issues, Issue{Element: f.ID, Code: "flow_dangling"})
		}
		if _, ok := p.Nodes[f.Target]; !ok {
			issues = append(issues, Issue{Element: f.ID, Code: "flow_dangling"})
		}
		if f.Condition != "" {
			if _, err := ParseCondition(f.Condition); err != nil {
				issues = append(issues, Issue{Element: f.ID, Code: "invalid_condition", Detail: err.Error()})
			}
		}
	}
	for _, id := range p.Order {
		n := p.Nodes[id]
		switch n.Type {
		case NodeStart:
			starts = append(starts, id)
			if len(n.Outgoing) != 1 {
				issues = append(issues, Issue{Element: id, Code: "start_needs_one_outgoing"})
			}
			if n.Cycle != "" {
				if _, err := ParseCycle(n.Cycle); errors.Is(err, ErrCycleTooFrequent) {
					issues = append(issues, Issue{Element: id, Code: "cycle_too_frequent", Detail: n.Cycle})
				} else if err != nil {
					issues = append(issues, Issue{Element: id, Code: "invalid_cycle", Detail: n.Cycle})
				}
			}
		case NodeEnd:
			ends++
			if len(n.Outgoing) > 0 {
				issues = append(issues, Issue{Element: id, Code: "end_has_outgoing"})
			}
		case NodeUserTask:
			if len(n.Outgoing) != 1 {
				issues = append(issues, Issue{Element: id, Code: "task_needs_one_outgoing"})
			}
			if n.Assignee == "" && len(n.CandidateUsers) == 0 && len(n.CandidateGroups) == 0 {
				issues = append(issues, Issue{Element: id, Code: "task_needs_assignment"})
			}
			for _, g := range n.CandidateGroups {
				if _, err := ParseGroup(g); err != nil {
					issues = append(issues, Issue{Element: id, Code: "invalid_candidate_group", Detail: g})
				}
			}
			for _, field := range n.FormFields {
				if !identRE.MatchString(field) {
					issues = append(issues, Issue{Element: id, Code: "invalid_form_field", Detail: field})
				}
			}
		case NodeServiceTask:
			if len(n.Outgoing) != 1 {
				issues = append(issues, Issue{Element: id, Code: "task_needs_one_outgoing"})
			}
			if code := validateAction(n); code != "" {
				issues = append(issues, Issue{Element: id, Code: code, Detail: n.Action})
			}
		case NodeExclusiveGateway:
			issues = append(issues, validateExclusive(p, n)...)
		case NodeParallelGateway:
			if len(n.Outgoing) == 0 {
				issues = append(issues, Issue{Element: id, Code: "gateway_needs_outgoing"})
			}
		case NodeWait:
			if len(n.Outgoing) != 1 {
				issues = append(issues, Issue{Element: id, Code: "wait_needs_one_outgoing"})
			}
			if n.Duration != "" {
				if _, err := ParseDuration(n.Duration); err != nil {
					issues = append(issues, Issue{Element: id, Code: "invalid_duration", Detail: n.Duration})
				}
			}
		case NodeBoundaryEvent:
			host, ok := p.Nodes[n.AttachedTo]
			if !ok || host.Type != NodeUserTask {
				issues = append(issues, Issue{Element: id, Code: "timer_needs_user_task"})
			}
			if len(n.Outgoing) != 1 {
				issues = append(issues, Issue{Element: id, Code: "timer_needs_one_outgoing"})
			}
			if n.Duration != "" {
				if _, err := ParseDuration(n.Duration); err != nil {
					issues = append(issues, Issue{Element: id, Code: "invalid_duration", Detail: n.Duration})
				}
			}
			if n.Remind != "" {
				lead, err := ParseDuration(n.Remind)
				due, dueErr := ParseDuration(n.Duration)
				if err != nil || (dueErr == nil && lead >= due) {
					issues = append(issues, Issue{Element: id, Code: "invalid_reminder", Detail: n.Remind})
				}
			}
		}
		if n.Type != NodeStart && n.Type != NodeBoundaryEvent && len(n.Incoming) == 0 {
			issues = append(issues, Issue{Element: id, Code: "unreachable"})
		}
	}
	switch len(starts) {
	case 0:
		issues = append(issues, Issue{Code: "no_start"})
	case 1:
		p.Start = starts[0]
		if start := p.Nodes[p.Start]; start.Cycle != "" {
			p.Schedule = &StartSchedule{Cycle: start.Cycle, Query: start.Query}
		}
	default:
		issues = append(issues, Issue{Code: "several_starts"})
	}
	if ends == 0 {
		issues = append(issues, Issue{Code: "no_end"})
	}
	return issues
}

func validateExclusive(p *Process, n *Node) []Issue {
	if len(n.Outgoing) == 0 {
		return []Issue{{Element: n.ID, Code: "gateway_needs_outgoing"}}
	}
	if len(n.Outgoing) == 1 {
		return nil
	}
	var issues []Issue
	for _, id := range n.Outgoing {
		if id != n.Default && p.Flows[id].Condition == "" {
			issues = append(issues, Issue{Element: id, Code: "branch_needs_condition"})
		}
	}
	return issues
}

func sortedFlows(p *Process) []*Flow {
	ids := make([]string, 0, len(p.Flows))
	for id := range p.Flows {
		ids = append(ids, id)
	}
	sortByDocument(p, ids)
	out := make([]*Flow, 0, len(ids))
	for _, id := range ids {
		out = append(out, p.Flows[id])
	}
	return out
}
