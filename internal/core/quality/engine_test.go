package quality_test

import (
	"context"
	"errors"
	"testing"

	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/quality"
	extquality "github.com/marmotdata/marmot/pkg/extension/quality"
)

type fixedFacts struct {
	domains    map[string]string
	documented map[string]bool
}

func (f fixedFacts) AssetDomains(context.Context, []string) (map[string]string, error) {
	return f.domains, nil
}

func (f fixedFacts) DocumentedAssets(context.Context, []string) (map[string]bool, error) {
	return f.documented, nil
}

func TestTheEngineJudgesABatchAndSaysWhereToContinue(t *testing.T) {
	source := &memorySource{assets: catalog(3)}
	engine := quality.NewEngine(registry(t), source, fixedFacts{domains: map[string]string{"id-a01": "dom-1"}}, nil)
	if profile := engine.Profile(); !profile.Enabled || profile.ID != "audit" {
		t.Fatalf("profile = %+v", profile)
	}
	if total, _ := engine.CountAssets(context.Background()); total != 3 {
		t.Fatalf("total = %d", total)
	}

	page, err := engine.Audit(context.Background(), extquality.Audit{Settings: quality.DefaultSettings(), Now: now, Limit: 2})
	if err != nil || len(page.Results) != 2 || page.Last != "id-a01" || page.Results[1].DomainID != "dom-1" {
		t.Fatalf("page = %+v, %v", page, err)
	}
	rest, err := engine.Audit(context.Background(), extquality.Audit{Settings: quality.DefaultSettings(), Now: now, After: page.Last, Limit: 2})
	if err != nil || len(rest.Results) != 1 || rest.Last != "id-a02" {
		t.Fatalf("rest = %+v, %v", rest, err)
	}
	end, err := engine.Audit(context.Background(), extquality.Audit{Settings: quality.DefaultSettings(), Now: now, After: rest.Last, Limit: 2})
	if err != nil || len(end.Results) != 0 || end.Last != "" {
		t.Fatalf("end = %+v, %v", end, err)
	}

	byID, err := engine.Audit(context.Background(), extquality.Audit{Settings: quality.DefaultSettings(), Now: now, IDs: []string{"id-a02"}})
	if err != nil || len(byID.Results) != 1 || byID.Results[0].AssetID != "id-a02" {
		t.Fatalf("by id = %+v, %v", byID, err)
	}
}

func TestTheEngineNeedsAProfileAndRefusesARuleOnAFieldItLacks(t *testing.T) {
	off, err := metamodel.LoadFile("")
	if err != nil {
		t.Fatal(err)
	}
	engine := quality.NewEngine(off, &memorySource{}, fixedFacts{}, nil)
	if _, err := engine.Audit(context.Background(), extquality.Audit{Limit: 1}); !errors.Is(err, extquality.ErrNoProfile) {
		t.Fatalf("err = %v", err)
	}

	engine = quality.NewEngine(registry(t), &memorySource{}, fixedFacts{}, nil)
	rule := extquality.Rule{ID: "mine", Name: "Mine", Dimension: "validity", Severity: "warning", Checks: []extquality.Condition{{Field: "nope", Op: "set"}}}
	var invalid *extquality.RuleError
	if err := engine.ValidateRule(rule); !errors.As(err, &invalid) {
		t.Fatalf("err = %v", err)
	}
	rule.Checks[0].Field = "classification"
	if err := engine.ValidateRule(rule); err != nil {
		t.Fatalf("err = %v", err)
	}
}
