package metamodel

import "testing"

func TestCoerceByDeclaredType(t *testing.T) {
	cases := []struct {
		field Field
		in    any
		want  any
		ok    bool
	}{
		{Field{Type: "boolean"}, "true", true, true},
		{Field{Type: "boolean"}, "false", false, true},
		{Field{Type: "boolean"}, true, true, true},
		{Field{Type: "boolean"}, "maybe", nil, false},
		{Field{Type: "boolean"}, "", nil, true},
		{Field{Type: "integer"}, "30", 30.0, true},
		{Field{Type: "integer"}, "3.5", nil, false},
		{Field{Type: "number"}, "1.5", 1.5, true},
		{Field{Type: "string"}, "true", "true", true}, // must not become bool
		{Field{Type: "enum", Values: []string{"public"}}, "public", "public", true},
		{Field{Type: "date"}, "2026-09-17", "2026-09-17", true},
		{Field{Type: "list", ItemType: "string"}, `["a","b"]`, []any{"a", "b"}, true},
		{Field{Type: "list", ItemType: "string"}, "solo", []any{"solo"}, true},
		{Field{Type: "string"}, nil, nil, true},
	}
	for _, tc := range cases {
		got, ok := Coerce(tc.field, tc.in)
		if ok != tc.ok {
			t.Fatalf("%s %v: ok=%v want %v", tc.field.Type, tc.in, ok, tc.ok)
		}
		if !ok {
			continue
		}
		switch want := tc.want.(type) {
		case []any:
			gotList, ok := got.([]any)
			if !ok || len(gotList) != len(want) {
				t.Fatalf("%s %v: got %#v", tc.field.Type, tc.in, got)
			}
			for i := range want {
				if gotList[i] != want[i] {
					t.Fatalf("%s %v: got %#v", tc.field.Type, tc.in, got)
				}
			}
		default:
			if got != tc.want {
				t.Fatalf("%s %v: got %#v want %#v", tc.field.Type, tc.in, got, tc.want)
			}
		}
	}
}
