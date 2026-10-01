package quality

import (
	"math"
	"slices"
	"strings"
	"time"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
)

type Status string

const (
	StatusCompliant    Status = "compliant"
	StatusWarning      Status = "warning"
	StatusNoncompliant Status = "noncompliant"
)

// UnassignedDomain is the key of the assets that belong to no domain.
const UnassignedDomain = "unassigned"

// outputFields are written by the audit, not read: publishing a score must not change the score.
var outputFields = []string{"quality_score", "quality_run", "quality_scored_at", "quality_dimensions", "quality_evaluated_at"}

// Issue is one finding on one field of one asset.
type Issue struct {
	FieldID  string   `json:"field_id"`
	Code     string   `json:"code"`
	RuleID   RuleID   `json:"rule_id"`
	Severity Severity `json:"severity" enums:"error,warning"`
	Section  string   `json:"section"`
	// Item is the position of the offending entry when the field is a list.
	Item *int `json:"item,omitempty"`
} // @name QualityIssue

// SectionStat counts how many of a section's fields an asset has, fills and fills validly.
type SectionStat struct{ Total, Filled, Valid int }

// AssetResult is what the audit concluded about one asset.
type AssetResult struct {
	AssetID      string  `json:"asset_id"`
	MRN          string  `json:"mrn"`
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	DomainID     string  `json:"domain_id"`
	Completeness float64 `json:"completeness"`
	Conformity   float64 `json:"conformity"`
	Quality      float64 `json:"quality"`
	Status       Status  `json:"status" enums:"compliant,warning,noncompliant"`
	IssueCount   int     `json:"issue_count"`
	// Issues is filled for the last successful run only.
	Issues []Issue `json:"issues,omitempty"`

	// Stub marks a placeholder created by lineage: it is counted apart and never stored or scored.
	Stub     bool                   `json:"-"`
	Sections map[string]SectionStat `json:"-"`
} // @name QualityAssetResult

// Auditor scores assets against the effective metamodel with the settings of one run.
type Auditor struct {
	registry *metamodel.Registry
	settings Settings
	today    string
	fields   []metamodel.Field
	byID     map[string]metamodel.Field
}

func NewAuditor(registry *metamodel.Registry, settings Settings, now time.Time) *Auditor {
	all := registry.Fields("asset")
	a := &Auditor{
		registry: registry,
		settings: settings,
		today:    now.UTC().Format(time.DateOnly),
		byID:     make(map[string]metamodel.Field, len(all)),
	}
	for _, f := range all {
		a.byID[f.ID] = f
		if !slices.Contains(outputFields, f.ID) {
			a.fields = append(a.fields, f)
		}
	}
	return a
}

func governed(f metamodel.Field) bool { return strings.HasPrefix(f.Storage, "metadata.") }

// unset: blank strings and empty lists count as unset, like the empty form fields that never reach the server.
func unset(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case []any:
		return len(v) == 0
	case []string:
		return len(v) == 0
	}
	return false
}

type finding struct {
	fieldID, code string
	item          *int
}

func ruleOf(code string) RuleID {
	switch code {
	case "required":
		return RuleRequired
	case "pii_coherence":
		return RulePIICoherence
	case "review_expired":
		return RuleReviewExpired
	case "external_link_invalid":
		return RuleExternalLinkInvalid
	case "external_link_empty":
		return RuleExternalLinkEmpty
	}
	return RuleValidation
}

func validDate(value string) bool {
	_, err := time.Parse(time.DateOnly, value)
	return err == nil
}

func round(value float64) float64 { return math.Round(value*10) / 10 }

func (a *Auditor) coherence(values map[string]any, links []asset.ExternalLink) []finding {
	var out []finding
	if pii, _ := values["contains_pii"].(bool); pii {
		for _, id := range []string{"classification", "data_steward"} {
			if _, ok := a.byID[id]; ok && unset(values[id]) {
				out = append(out, finding{fieldID: id, code: "pii_coherence"})
			}
		}
	}
	if review, ok := values["next_review"].(string); ok && validDate(review) && review < a.today {
		out = append(out, finding{fieldID: "next_review", code: "review_expired"})
	}
	for i, link := range links {
		if strings.TrimSpace(link.Name) == "" && strings.TrimSpace(link.URL) == "" {
			item := i
			out = append(out, finding{fieldID: "external_links", code: "external_link_empty", item: &item})
		}
	}
	return out
}

// Audit scores one asset. A stub is judged on its native attributes only, as the server exempts
// it from every governed field.
func (a *Auditor) Audit(as *asset.Asset) AssetResult {
	values := asset.MetamodelValues(a.registry, as)
	result := AssetResult{AssetID: as.ID, Type: as.Type, Stub: as.IsStub, DomainID: UnassignedDomain, Sections: map[string]SectionStat{}}
	if as.MRN != nil {
		result.MRN = *as.MRN
	}
	if as.Name != nil {
		result.Name = *as.Name
	}

	var findings []finding
	total, filled, valid, anyRequired := 0, 0, 0, false
	for _, f := range a.fields {
		if (as.IsStub && governed(f)) || !f.InScope(values) {
			continue
		}
		total++
		anyRequired = anyRequired || f.Required
		stat := result.Sections[f.Presentation.Section]
		stat.Total++
		value := values[f.ID]
		if unset(value) {
			if f.Required && f.Derive == nil {
				findings = append(findings, finding{fieldID: f.ID, code: "required"})
			}
			result.Sections[f.Presentation.Section] = stat
			continue
		}
		filled++
		stat.Filled++
		if code := metamodel.ValidateValue(f, value); code != "" {
			findings = append(findings, finding{fieldID: f.ID, code: code})
		} else {
			valid++
			stat.Valid++
		}
		result.Sections[f.Presentation.Section] = stat
	}
	if !as.IsStub {
		findings = append(findings, a.coherence(values, as.ExternalLinks)...)
	}

	hasError := false
	for _, found := range findings {
		id := ruleOf(found.code)
		rule := a.settings.Rules[id]
		if !rule.Enabled {
			continue
		}
		hasError = hasError || rule.Severity == SeverityError
		result.Issues = append(result.Issues, Issue{
			FieldID: found.fieldID, Code: found.code, RuleID: id, Severity: rule.Severity,
			Section: a.byID[found.fieldID].Presentation.Section, Item: found.item,
		})
	}
	result.IssueCount = len(result.Issues)

	completeness := 100.0
	if total > 0 {
		completeness = float64(filled) / float64(total) * 100
	}
	conformity := 100.0
	switch {
	case filled > 0:
		conformity = float64(valid) / float64(filled) * 100
	case anyRequired:
		conformity = 0
	}
	quality := a.settings.Weights.score(completeness, conformity)
	result.Completeness, result.Conformity, result.Quality = round(completeness), round(conformity), round(quality)
	switch {
	case quality >= a.settings.Thresholds.Compliant && !hasError:
		result.Status = StatusCompliant
	case quality >= a.settings.Thresholds.Warning:
		result.Status = StatusWarning
	default:
		result.Status = StatusNoncompliant
	}
	return result
}

func (w Weights) score(completeness, conformity float64) float64 {
	total := w.Completeness + w.Conformity
	if total == 0 {
		total = 1
	}
	return (completeness*w.Completeness + conformity*w.Conformity) / total
}
