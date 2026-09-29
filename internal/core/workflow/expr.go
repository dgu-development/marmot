package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Condition is a conjunction of comparisons on instance variables, such as
// `decision == "approved"` or `${decision != 'rejected' && reviewed == true}`.
// There is no other syntax: a diagram cannot carry code.
type Condition struct {
	Terms []Comparison
}

// Comparison is one `variable op literal` term.
type Comparison struct {
	Variable string
	Equal    bool
	Value    string
}

var (
	identRE     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,63}$`)
	comparisonR = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_.]{0,63})\s*(==|!=)\s*(.+?)\s*$`)
)

// ParseCondition parses the condition of a sequence flow.
func ParseCondition(text string) (*Condition, error) {
	body := strings.TrimSpace(text)
	if strings.HasPrefix(body, "${") && strings.HasSuffix(body, "}") {
		body = strings.TrimSpace(body[2 : len(body)-1])
	}
	if body == "" {
		return nil, errors.New("empty condition")
	}
	if strings.Contains(body, "||") {
		return nil, errors.New("only && is supported")
	}
	c := &Condition{}
	for _, part := range strings.Split(body, "&&") {
		m := comparisonR.FindStringSubmatch(part)
		if m == nil {
			return nil, fmt.Errorf("expected `variable == value`, got %q", strings.TrimSpace(part))
		}
		value, err := literal(m[3])
		if err != nil {
			return nil, err
		}
		c.Terms = append(c.Terms, Comparison{Variable: m[1], Equal: m[2] == "==", Value: value})
	}
	return c, nil
}

func literal(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && (raw[0] == '\'' || raw[0] == '"') && raw[len(raw)-1] == raw[0] {
		inner := raw[1 : len(raw)-1]
		if strings.ContainsRune(inner, rune(raw[0])) {
			return "", fmt.Errorf("unbalanced quotes in %s", raw)
		}
		return inner, nil
	}
	if raw == "true" || raw == "false" {
		return raw, nil
	}
	if _, err := strconv.ParseFloat(raw, 64); err == nil {
		return raw, nil
	}
	if identRE.MatchString(raw) {
		return raw, nil
	}
	return "", fmt.Errorf("unsupported value %s", raw)
}

// Eval reports whether every term holds. A missing variable compares as "".
func (c *Condition) Eval(vars map[string]string) bool {
	for _, t := range c.Terms {
		if (vars[t.Variable] == t.Value) != t.Equal {
			return false
		}
	}
	return true
}

// GroupKind is how a candidate group entry names people.
type GroupKind string

const (
	// GroupTeam is `team:<id or name>`.
	GroupTeam GroupKind = "team"
	// GroupRole is `role:<domain role>` for the domain of the instance target,
	// or `role:<domain role>@<domain id>` for a fixed domain.
	GroupRole GroupKind = "role"
)

// Group is one parsed camunda:candidateGroups entry.
type Group struct {
	Kind     GroupKind
	Value    string
	DomainID string
}

var domainRoles = map[string]bool{"domain_admin": true, "steward": true, "reader": true}

// ParseGroup parses one candidate group entry.
func ParseGroup(raw string) (Group, error) {
	kind, value, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok || value == "" {
		return Group{}, fmt.Errorf("expected team:<name> or role:<role>, got %q", raw)
	}
	switch GroupKind(kind) {
	case GroupTeam:
		return Group{Kind: GroupTeam, Value: value}, nil
	case GroupRole:
		role, domainID, _ := strings.Cut(value, "@")
		if !domainRoles[role] {
			return Group{}, fmt.Errorf("unknown domain role %q", role)
		}
		return Group{Kind: GroupRole, Value: role, DomainID: domainID}, nil
	}
	return Group{}, fmt.Errorf("unknown group kind %q", kind)
}

var durationRE = regexp.MustCompile(`^P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// ParseDuration reads the ISO 8601 durations a timer uses: weeks, days,
// hours, minutes and seconds (P3D, PT4H, P1DT12H). Years and months are
// rejected: their length depends on the calendar.
func ParseDuration(text string) (time.Duration, error) {
	m := durationRE.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil || text == "P" || strings.HasSuffix(text, "T") {
		return 0, fmt.Errorf("unsupported duration %q", text)
	}
	units := []time.Duration{7 * 24 * time.Hour, 24 * time.Hour, time.Hour, time.Minute, time.Second}
	var total time.Duration
	for i, unit := range units {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return 0, err
		}
		total += time.Duration(n) * unit
	}
	if total <= 0 {
		return 0, fmt.Errorf("duration %q must be positive", text)
	}
	return total, nil
}

// Actions a service task may run. Each one is implemented by the platform.
const (
	// ActionNotify sends an in-app notification: dgu:message, and dgu:to set
	// to "initiator" (default) or "participants".
	ActionNotify = "notify"
	// ActionSetField writes a governed field of the target asset through the
	// metamodel: dgu:field and dgu:value (raw text; PatchFields coerces by type).
	ActionSetField = "set_field"
	// ActionAddTag adds dgu:tag to the target asset.
	ActionAddTag = "add_tag"
	// ActionRemoveTag removes dgu:tag from the target asset.
	ActionRemoveTag = "remove_tag"
	// ActionClearField clears a governed field (PatchFields with null).
	ActionClearField = "clear_field"
)

func validateAction(n *Node) string {
	switch n.Action {
	case "":
		return "service_needs_action"
	case ActionNotify:
		if strings.TrimSpace(n.Args["message"]) == "" {
			return "action_needs_message"
		}
		if to := n.Args["to"]; to != "" && to != "initiator" && to != "participants" {
			return "invalid_recipients"
		}
	case ActionSetField:
		if !identRE.MatchString(n.Args["field"]) {
			return "action_needs_field"
		}
		if _, ok := n.Args["value"]; !ok {
			return "action_needs_value"
		}
	case ActionClearField:
		if !identRE.MatchString(n.Args["field"]) {
			return "action_needs_field"
		}
	case ActionAddTag, ActionRemoveTag:
		if strings.TrimSpace(n.Args["tag"]) == "" {
			return "action_needs_tag"
		}
	default:
		return "unknown_action"
	}
	return ""
}

// FieldValue turns a BPMN dgu:value into a JSON literal when it parses as one.
// Prefer metamodel.Coerce when the field type is known (PatchFields does).
func FieldValue(raw string) any {
	trimmed := strings.TrimSpace(raw)
	var v any
	if trimmed != "" && json.Unmarshal([]byte(trimmed), &v) == nil {
		if _, isMap := v.(map[string]any); !isMap {
			return v
		}
	}
	return raw
}
