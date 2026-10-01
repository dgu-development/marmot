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
	// Dimensions are the checks the asset meets, in the order of dimensionOrder; they are written
	// to the asset with its score and are not stored with the results.
	Dimensions []string `json:"-"`
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
// it from every governed field. Whether the asset has documentation is judged from its ingested
// markdown alone; AuditWithDocs knows about the pages written in the platform too.
func (a *Auditor) Audit(as *asset.Asset) AssetResult {
	return a.AuditWithDocs(as, false)
}

// AuditWithDocs is Audit given whether the asset has documentation pages of its own.
func (a *Auditor) AuditWithDocs(as *asset.Asset, hasPages bool) AssetResult {
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
		if as.IsStub && governed(f) {
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
	if !as.IsStub {
		result.Dimensions = a.dimensions(as, values, findings, hasPages || ingestedBody(as))
	}

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

// dimensionOrder is the order the checks are listed and written in.
var dimensionOrder = []string{"description", "tags", "ownership", "classification", "review", "documentation", "resource", "completeness", "conformity"}

// ingestedBody is whether a source (an Obsidian note, say) brought the asset a markdown body, which
// the documentation tab shows as its first page.
func ingestedBody(as *asset.Asset) bool {
	dgu, _ := as.Metadata["dgu"].(map[string]any)
	body, _ := dgu["body"].(string)
	return strings.TrimSpace(body) != ""
}

// valueCodes are the findings about a value, as opposed to the coherence rules and the gaps.
func valueCode(code string) bool {
	switch code {
	case "required", "pii_coherence", "review_expired", "external_link_invalid", "external_link_empty":
		return false
	}
	return true
}

// dimensions lists the checks an asset meets. `documentation` is having documentation to read (pages
// or an ingested body) and `resource` is having a link out to a resource that documents it. A check about a field the profile does not have does
// not apply and is left out, so a profile without a steward never fails ownership.
func (a *Auditor) dimensions(as *asset.Asset, values map[string]any, findings []finding, documented bool) []string {
	has := func(id string) bool { _, ok := a.byID[id]; return ok }
	met := map[string]bool{
		"description":    (as.UserDescription != nil && strings.TrimSpace(*as.UserDescription) != "") || (as.Description != nil && strings.TrimSpace(*as.Description) != ""),
		"tags":           !unset(as.Tags),
		"ownership":      has("data_steward") && !unset(values["data_steward"]),
		"classification": has("classification") && !unset(values["classification"]),
		"documentation":  documented,
		"resource":       false,
		"completeness":   true,
		"conformity":     true,
	}
	review, _ := values["next_review"].(string)
	met["review"] = has("next_review") && validDate(review) && review >= a.today
	for _, link := range as.ExternalLinks {
		if strings.TrimSpace(link.URL) != "" {
			met["resource"] = true
			break
		}
	}
	for _, found := range findings {
		if found.code == "required" {
			met["completeness"] = false
		}
		if valueCode(found.code) {
			met["conformity"] = false
		}
	}
	out := []string{}
	for _, id := range dimensionOrder {
		if met[id] {
			out = append(out, id)
		}
	}
	return out
}
