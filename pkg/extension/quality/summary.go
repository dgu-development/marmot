package quality

import (
	"cmp"
	"math"
	"slices"
)

const topFields = 8

// FieldCount is how many findings a field has.
type FieldCount struct {
	FieldID string `json:"field_id"`
	Count   int    `json:"count"`
} // @name QualityFieldCount

// GroupStat is the mean quality of a group of assets: a section, an asset type or a domain.
type GroupStat struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
	Count int     `json:"count"`
} // @name QualityGroupStat

// Summary is what a run concluded overall. Stubs are counted apart: they are placeholders created
// by lineage, not catalogued assets, so they would only drag the averages down.
type Summary struct {
	TotalAssets int     `json:"total_assets"`
	Stubs       int     `json:"stubs"`
	Quality     float64 `json:"quality"`
	// ByDimension is the mean score of each quality dimension, over the assets it applies to.
	ByDimension  []GroupStat    `json:"by_dimension"`
	TotalIssues  int            `json:"total_issues"`
	StatusCounts map[Status]int `json:"status_counts"`
	TopFields    []FieldCount   `json:"top_fields"`
	BySection    []GroupStat    `json:"by_section"`
	ByType       []GroupStat    `json:"by_type"`
	// ByDomain is one more aggregate of the run, not a filter of it; the assets of no domain are
	// under "unassigned".
	ByDomain []GroupStat `json:"by_domain"`
} // @name QualitySummary

type mean struct {
	sum   float64
	count int
}

func (m *mean) add(value float64) { m.sum += value; m.count++ }

func (m mean) value() float64 {
	if m.count == 0 {
		return 0
	}
	return round(m.sum / float64(m.count))
}

// Aggregator folds results into a Summary one batch at a time, so a run never holds the catalog.
type Aggregator struct {
	weights                  Weights
	stubs, issues            int
	quality                  mean
	dimensions               map[string]*mean
	status                   map[Status]int
	fields                   map[string]int
	sections, types, domains map[string]*mean
}

func NewAggregator(weights Weights) *Aggregator {
	return &Aggregator{
		weights:    weights,
		status:     map[Status]int{StatusCompliant: 0, StatusWarning: 0, StatusNoncompliant: 0},
		dimensions: map[string]*mean{},
		fields:     map[string]int{},
		sections:   map[string]*mean{},
		types:      map[string]*mean{},
		domains:    map[string]*mean{},
	}
}

func bump(groups map[string]*mean, key string, value float64) {
	m := groups[key]
	if m == nil {
		m = &mean{}
		groups[key] = m
	}
	m.add(value)
}

func (a *Aggregator) Add(r AssetResult) {
	if r.Stub {
		a.stubs++
		return
	}
	a.status[r.Status]++
	a.quality.add(r.Quality)
	for dimension, score := range r.Scores {
		bump(a.dimensions, dimension, score)
	}
	a.issues += r.IssueCount
	for _, issue := range r.Issues {
		a.fields[issue.FieldID]++
	}
	for key, stat := range r.Sections {
		scores := map[string]float64{DimensionCompleteness: 100, DimensionValidity: 100}
		if stat.Total > 0 {
			scores[DimensionCompleteness] = float64(stat.Filled) / float64(stat.Total) * 100
		}
		if stat.Filled > 0 {
			scores[DimensionValidity] = float64(stat.Valid) / float64(stat.Filled) * 100
		}
		bump(a.sections, key, a.weights.Mix(scores))
	}
	bump(a.types, r.Type, r.Quality)
	bump(a.domains, r.DomainID, r.Quality)
}

// dimensionRanking lists the dimensions that applied to some asset, in the order of the metamodel.
func dimensionRanking(groups map[string]*mean) []GroupStat {
	out := []GroupStat{}
	for _, dimension := range Dimensions {
		if m := groups[dimension]; m != nil {
			out = append(out, GroupStat{Key: dimension, Value: m.value(), Count: m.count})
		}
	}
	return out
}

func ranked(groups map[string]*mean) []GroupStat {
	out := make([]GroupStat, 0, len(groups))
	for key, m := range groups {
		out = append(out, GroupStat{Key: key, Value: m.value(), Count: m.count})
	}
	slices.SortFunc(out, func(x, y GroupStat) int {
		return cmp.Or(cmp.Compare(x.Value, y.Value), cmp.Compare(x.Key, y.Key))
	})
	return out
}

func (a *Aggregator) Summary() Summary {
	top := make([]FieldCount, 0, len(a.fields))
	for id, count := range a.fields {
		top = append(top, FieldCount{FieldID: id, Count: count})
	}
	slices.SortFunc(top, func(x, y FieldCount) int {
		return cmp.Or(cmp.Compare(y.Count, x.Count), cmp.Compare(x.FieldID, y.FieldID))
	})
	if len(top) > topFields {
		top = top[:topFields]
	}
	status := make(map[Status]int, len(a.status))
	for k, v := range a.status {
		status[k] = v
	}
	return Summary{
		TotalAssets:  a.quality.count,
		Stubs:        a.stubs,
		Quality:      a.quality.value(),
		ByDimension:  dimensionRanking(a.dimensions),
		TotalIssues:  a.issues,
		StatusCounts: status,
		TopFields:    top,
		BySection:    ranked(a.sections),
		ByType:       ranked(a.types),
		ByDomain:     ranked(a.domains),
	}
}

func round(value float64) float64 { return math.Round(value*10) / 10 }
