package readscope

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestQueriesAreUntouchedWhenNothingIsHidden(t *testing.T) {
	ctx := context.Background()
	var params []any
	if got := Cond(ctx, Asset, "id", &params); got != "" || len(params) != 0 {
		t.Fatalf("no resolver: %q %v", got, params)
	}
	SetResolver(func(context.Context) ([]string, error) { return nil, nil })
	t.Cleanup(func() { SetResolver(nil) })
	if got := Shadow(ctx, "SELECT 1 FROM assets", &params); got != "SELECT 1 FROM assets" || len(params) != 0 {
		t.Fatalf("nothing hidden: %q %v", got, params)
	}
}

func TestHiddenDomainsBecomeAConditionWithItsParameter(t *testing.T) {
	SetResolver(func(context.Context) ([]string, error) { return []string{"d1"}, nil })
	t.Cleanup(func() { SetResolver(nil) })
	ctx := context.Background()

	params := []any{"x"}
	got := Cond(ctx, GlossaryTerm, "gt.id", &params)
	if got != "gt.id NOT IN (SELECT glossary_term_id FROM public.glossary_term_domains WHERE domain_id = ANY($2::uuid[]))" || len(params) != 2 {
		t.Fatalf("%q %v", got, params)
	}
	if got := Cond(Unrestricted(ctx), Asset, "id", &params); got != "" || len(params) != 2 {
		t.Fatalf("unrestricted work must see everything: %q", got)
	}
}

func TestShadowKeepsTheShapeOfTheQuery(t *testing.T) {
	SetResolver(func(context.Context) ([]string, error) { return []string{"d1"}, nil })
	t.Cleanup(func() { SetResolver(nil) })
	ctx := context.Background()
	const cte = "assets AS NOT MATERIALIZED (SELECT * FROM public.assets WHERE id NOT IN (SELECT asset_id FROM public.asset_domains WHERE domain_id = ANY($1::uuid[])))"

	for name, tc := range map[string]struct{ query, want string }{
		"select":    {"\n\tSELECT 1 FROM assets", "WITH " + cte + " SELECT 1 FROM assets"},
		"with":      {"WITH x AS (SELECT id FROM assets) SELECT * FROM x", "WITH " + cte + ", x AS (SELECT id FROM assets) SELECT * FROM x"},
		"recursive": {"with recursive x AS (SELECT id FROM assets) SELECT * FROM x", "WITH RECURSIVE " + cte + ", x AS (SELECT id FROM assets) SELECT * FROM x"},
		"other":     {"SELECT 1 FROM users WHERE updated_at > now()", "SELECT 1 FROM users WHERE updated_at > now()"},
		"write":     {"WITH gone AS (DELETE FROM assets RETURNING id) SELECT * FROM gone", "WITH gone AS (DELETE FROM assets RETURNING id) SELECT * FROM gone"},
		"lock":      {"SELECT id FROM assets FOR UPDATE", "SELECT id FROM assets FOR UPDATE"},
	} {
		var params []any
		if got := Shadow(ctx, tc.query, &params); got != tc.want || (got != tc.query) != (len(params) == 1) {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestAScopeThatCannotBeResolvedHidesEverything(t *testing.T) {
	SetResolver(func(context.Context) ([]string, error) { return nil, errors.New("down") })
	t.Cleanup(func() { SetResolver(nil) })
	var params []any
	if got := Cond(context.Background(), Asset, "id", &params); got != "FALSE" {
		t.Fatalf("cond = %q", got)
	}
	if got := Shadow(context.Background(), "SELECT 1 FROM assets", &params); !strings.Contains(got, "WHERE FALSE)") {
		t.Fatalf("shadow = %q", got)
	}
}
