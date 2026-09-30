package metamodel

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Coerce turns a wire value into what Validate expects for f.
// HTML forms and BPMN dgu:value arrive as strings; already-typed values pass through.
// An empty string becomes nil (clear). ok is false when the string cannot match f.Type.
func Coerce(f Field, value any) (any, bool) {
	if value == nil {
		return nil, true
	}
	s, isStr := value.(string)
	if !isStr {
		return value, true
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, true
	}
	switch f.Type {
	case "boolean":
		switch s {
		case "true":
			return true, true
		case "false":
			return false, true
		default:
			return nil, false
		}
	case "integer", "number":
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, false
		}
		if f.Type == "integer" && math.Trunc(n) != n {
			return nil, false
		}
		return n, true
	case "list":
		var items []any
		if json.Unmarshal([]byte(s), &items) == nil {
			return items, true
		}
		return []any{s}, true
	default:
		// string, enum, date — keep text even when it looks like a JSON literal
		return s, true
	}
}
