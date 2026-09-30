package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

// maxSteps bounds one advance, so a cycle with no waiting task fails the
// instance instead of spinning.
const maxSteps = 1000

// State is what an instance keeps between steps.
type State struct {
	Tokens []Token `json:"tokens"`
	// Joins records, per parallel gateway, the incoming flows already arrived.
	Joins map[string][]string `json:"joins,omitempty"`
	Vars  map[string]string   `json:"vars"`
}

// Token waits on a user task or a timed wait. Tokens only exist while
// waiting: everything else runs to the next wait within one advance.
type Token struct {
	ID   string `json:"id"`
	Node string `json:"node"`
}

// Executor runs the service task actions. The engine never interprets an
// action itself.
type Executor interface {
	Execute(ctx context.Context, node *Node, vars map[string]string) error
}

// Step lists what one advance did, for the service to persist and notify.
type Step struct {
	NewTasks       []NewTask
	CancelledTasks []string
	// ReleasedWaits are the tokens of waits whose timer ran out.
	ReleasedWaits []string
	Events        []StepEvent
	Done          bool
	// Failure is set when the instance cannot go on; Done is then false.
	Failure *Failure
}

// NewTask is a user task, or a timed wait, the instance now waits on.
type NewTask struct {
	TokenID string
	Node    *Node
	// DueAt and Timer come from the earliest boundary timer on the task, or
	// from the wait itself. RemindAt is when its candidates get a reminder.
	DueAt    *time.Time
	Timer    string
	RemindAt *time.Time
}

// StepEvent is an audit entry: a node reached or a flow taken.
type StepEvent struct {
	Type    string         `json:"type"`
	Element string         `json:"element,omitempty"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// Failure stops an instance; Code is stable for clients.
type Failure struct {
	Element string
	Code    string
	Err     error
}

func (f *Failure) Error() string {
	if f.Err != nil {
		return fmt.Sprintf("%s at %s: %v", f.Code, f.Element, f.Err)
	}
	return fmt.Sprintf("%s at %s", f.Code, f.Element)
}

// Runner advances one instance of a process.
type Runner struct {
	Process  *Process
	Executor Executor
	Now      func() time.Time
}

type arrival struct {
	node string
	via  string
}

// NewState returns the empty state of a fresh instance.
func NewState(vars map[string]string) *State {
	if vars == nil {
		vars = map[string]string{}
	}
	return &State{Vars: vars, Joins: map[string][]string{}}
}

// Start runs a fresh instance from its start event to the first waits.
func (r *Runner) Start(ctx context.Context, st *State) *Step {
	step := &Step{}
	step.Events = append(step.Events, StepEvent{Type: "started", Element: r.Process.Start})
	r.run(ctx, st, step, []arrival{{node: r.Process.Start}})
	return step
}

// Complete finishes the user task a token waits on, with the given variables.
func (r *Runner) Complete(ctx context.Context, st *State, tokenID string, vars map[string]string) (*Step, error) {
	i := slices.IndexFunc(st.Tokens, func(t Token) bool { return t.ID == tokenID })
	if i < 0 {
		return nil, errors.New("no such token")
	}
	node := r.Process.Nodes[st.Tokens[i].Node]
	st.Tokens = slices.Delete(st.Tokens, i, i+1)
	for k, v := range vars {
		st.Vars[k] = v
	}
	step := &Step{}
	step.Events = append(step.Events, StepEvent{Type: "task_completed", Element: node.ID})
	r.run(ctx, st, step, r.follow(node))
	return step, nil
}

// FireTimer runs a boundary timer on the task a token waits on. An
// interrupting timer takes the token away from the task; a non-interrupting
// one starts a second path and leaves the task open.
func (r *Runner) FireTimer(ctx context.Context, st *State, tokenID, timerID string) (*Step, error) {
	timer := r.Process.Nodes[timerID]
	if timer == nil || (timer.Type != NodeBoundaryEvent && timer.Type != NodeWait) {
		return nil, errors.New("no such timer")
	}
	i := slices.IndexFunc(st.Tokens, func(t Token) bool { return t.ID == tokenID })
	if i < 0 {
		return nil, errors.New("no such token")
	}
	step := &Step{}
	step.Events = append(step.Events, StepEvent{Type: "timer_fired", Element: timerID})
	if timer.Type == NodeWait {
		if st.Tokens[i].Node != timerID {
			return nil, errors.New("the token does not wait on this timer")
		}
		st.Tokens = slices.Delete(st.Tokens, i, i+1)
		step.ReleasedWaits = append(step.ReleasedWaits, tokenID)
	} else if timer.CancelActivity {
		st.Tokens = slices.Delete(st.Tokens, i, i+1)
		step.CancelledTasks = append(step.CancelledTasks, tokenID)
	}
	r.run(ctx, st, step, r.follow(timer))
	return step, nil
}

func (r *Runner) follow(n *Node) []arrival {
	out := make([]arrival, 0, len(n.Outgoing))
	for _, id := range n.Outgoing {
		out = append(out, arrival{node: r.Process.Flows[id].Target, via: id})
	}
	return out
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) run(ctx context.Context, st *State, step *Step, queue []arrival) {
	if st.Joins == nil {
		st.Joins = map[string][]string{}
	}
	for steps := 0; len(queue) > 0; steps++ {
		if steps >= maxSteps {
			step.Failure = &Failure{Code: "loop_limit"}
			return
		}
		a := queue[0]
		queue = queue[1:]
		n := r.Process.Nodes[a.node]
		switch n.Type {
		case NodeStart:
			queue = append(queue, r.follow(n)...)
		case NodeEnd:
			step.Events = append(step.Events, StepEvent{Type: "ended", Element: n.ID})
			if n.Terminate {
				for _, t := range st.Tokens {
					step.CancelledTasks = append(step.CancelledTasks, t.ID)
				}
				st.Tokens = nil
				st.Joins = map[string][]string{}
				step.Done = true
				return
			}
		case NodeUserTask:
			token := Token{ID: uuid.NewString(), Node: n.ID}
			st.Tokens = append(st.Tokens, token)
			task := NewTask{TokenID: token.ID, Node: n}
			r.attachTimer(&task)
			step.NewTasks = append(step.NewTasks, task)
			step.Events = append(step.Events, StepEvent{Type: "task_created", Element: n.ID})
		case NodeServiceTask:
			if r.Executor == nil {
				step.Failure = &Failure{Element: n.ID, Code: "no_executor"}
				return
			}
			if err := r.Executor.Execute(ctx, n, st.Vars); err != nil {
				step.Failure = &Failure{Element: n.ID, Code: "action_failed", Err: err}
				return
			}
			step.Events = append(step.Events, StepEvent{Type: "action_done", Element: n.ID, Detail: map[string]any{"action": n.Action}})
			queue = append(queue, r.follow(n)...)
		case NodeExclusiveGateway:
			next, ok := r.choose(n, st.Vars)
			if !ok {
				step.Failure = &Failure{Element: n.ID, Code: "no_branch"}
				return
			}
			step.Events = append(step.Events, StepEvent{Type: "branch_taken", Element: next})
			queue = append(queue, arrival{node: r.Process.Flows[next].Target, via: next})
		case NodeParallelGateway:
			if len(n.Incoming) > 1 {
				arrived := st.Joins[n.ID]
				if a.via != "" && !slices.Contains(arrived, a.via) {
					arrived = append(arrived, a.via)
				}
				if len(arrived) < len(n.Incoming) {
					st.Joins[n.ID] = arrived
					continue
				}
				delete(st.Joins, n.ID)
			}
			queue = append(queue, r.follow(n)...)
		case NodeWait:
			d, err := ParseDuration(n.Duration)
			if err != nil {
				step.Failure = &Failure{Element: n.ID, Code: "invalid_duration", Err: err}
				return
			}
			due := r.now().Add(d)
			token := Token{ID: uuid.NewString(), Node: n.ID}
			st.Tokens = append(st.Tokens, token)
			step.NewTasks = append(step.NewTasks, NewTask{TokenID: token.ID, Node: n, DueAt: &due, Timer: n.ID})
			step.Events = append(step.Events, StepEvent{Type: "wait_started", Element: n.ID})
		case NodeBoundaryEvent:
			queue = append(queue, r.follow(n)...)
		}
	}
	if len(st.Tokens) == 0 && len(st.Joins) == 0 {
		step.Done = true
	} else if len(st.Tokens) == 0 {
		step.Failure = &Failure{Code: "stuck_join"}
	}
}

func (r *Runner) choose(n *Node, vars map[string]string) (string, bool) {
	for _, id := range n.Outgoing {
		if id == n.Default {
			continue
		}
		f := r.Process.Flows[id]
		if f.Condition == "" {
			return id, true
		}
		c, err := ParseCondition(f.Condition)
		if err == nil && c.Eval(vars) {
			return id, true
		}
	}
	if n.Default != "" {
		return n.Default, true
	}
	return "", false
}

func (r *Runner) attachTimer(task *NewTask) {
	for _, id := range r.Process.Order {
		b := r.Process.Nodes[id]
		if b.Type != NodeBoundaryEvent || b.AttachedTo != task.Node.ID {
			continue
		}
		d, err := ParseDuration(b.Duration)
		if err != nil {
			continue
		}
		due := r.now().Add(d)
		if task.DueAt == nil || due.Before(*task.DueAt) {
			task.DueAt = &due
			task.Timer = b.ID
			task.RemindAt = nil
			if lead, err := ParseDuration(b.Remind); err == nil && lead < d {
				remind := due.Add(-lead)
				task.RemindAt = &remind
			}
		}
	}
}
