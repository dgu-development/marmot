package quality_test

import (
	"fmt"
	"testing"

	"github.com/marmotdata/marmot/internal/core/quality"
)

func TestSummaryCountsStubsApartAndAveragesTheRest(t *testing.T) {
	agg := quality.NewAggregator(quality.DefaultSettings().Weights)
	agg.Add(quality.AssetResult{Stub: true, Quality: 0, Type: "table"})
	agg.Add(quality.AssetResult{Quality: 100, Scores: map[string]float64{"completeness": 100, "validity": 100}, Status: quality.StatusCompliant, Type: "table", DomainID: "d1",
		Sections: map[string]quality.SectionStat{"general": {Total: 2, Filled: 2, Valid: 2}}})
	agg.Add(quality.AssetResult{Quality: 50, Scores: map[string]float64{"completeness": 40, "validity": 60}, Status: quality.StatusNoncompliant, Type: "topic", DomainID: quality.UnassignedDomain, IssueCount: 2,
		Issues:   []quality.Issue{{FieldID: "a"}, {FieldID: "a"}},
		Sections: map[string]quality.SectionStat{"general": {Total: 2, Filled: 0, Valid: 0}}})

	s := agg.Summary()
	if s.TotalAssets != 2 || s.Stubs != 1 || s.Quality != 75 || s.TotalIssues != 2 {
		t.Fatalf("%+v", s)
	}
	if len(s.ByDimension) != 2 || s.ByDimension[0] != (quality.GroupStat{Key: "completeness", Value: 70, Count: 2}) || s.ByDimension[1].Value != 80 {
		t.Fatalf("by dimension: %+v", s.ByDimension)
	}
	if s.StatusCounts[quality.StatusCompliant] != 1 || s.StatusCounts[quality.StatusNoncompliant] != 1 || s.StatusCounts[quality.StatusWarning] != 0 {
		t.Fatalf("status = %v", s.StatusCounts)
	}
	if len(s.ByType) != 2 || s.ByType[0].Key != "topic" || s.ByType[0].Value != 50 {
		t.Fatalf("by type, weakest first: %+v", s.ByType)
	}
	if len(s.ByDomain) != 2 || s.ByDomain[0].Key != quality.UnassignedDomain {
		t.Fatalf("by domain: %+v", s.ByDomain)
	}
	// An asset with nothing filled has full validity by default, so its section scores (0+100)/2 = 50.
	if len(s.BySection) != 1 || s.BySection[0].Value != 75 || s.BySection[0].Count != 2 {
		t.Fatalf("by section: %+v", s.BySection)
	}
	if len(s.TopFields) != 1 || s.TopFields[0] != (quality.FieldCount{FieldID: "a", Count: 2}) {
		t.Fatalf("top fields: %+v", s.TopFields)
	}
}

func TestTopFieldsAreCappedAndOrdered(t *testing.T) {
	agg := quality.NewAggregator(quality.DefaultSettings().Weights)
	for i := 0; i < 12; i++ {
		var issues []quality.Issue
		for j := 0; j <= i; j++ {
			issues = append(issues, quality.Issue{FieldID: fmt.Sprintf("f%02d", i)})
		}
		agg.Add(quality.AssetResult{Issues: issues, IssueCount: len(issues)})
	}
	top := agg.Summary().TopFields
	if len(top) != 8 || top[0].FieldID != "f11" || top[7].FieldID != "f04" {
		t.Fatalf("%+v", top)
	}
}

func TestAnEmptyRunSummarisesToZeros(t *testing.T) {
	s := quality.NewAggregator(quality.DefaultSettings().Weights).Summary()
	if s.TotalAssets != 0 || s.Quality != 0 || s.TopFields == nil || s.ByType == nil || len(s.StatusCounts) != 3 {
		t.Fatalf("%+v", s)
	}
}
