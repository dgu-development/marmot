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
var outputFields = []string{
	"metadata_quality_score", "metadata_quality_dimensions", "metadata_quality_evaluated_at",
	// The names these fields had before; a profile that still declares them is not audited on them.
	"quality_score", "quality_dimensions", "quality_evaluated_at", "quality_run", "quality_scored_at",
}

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
	AssetID  string `json:"asset_id"`
	MRN      string `json:"mrn"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	DomainID string `json:"domain_id"`
	// Scores are the score of each quality dimension that applies to the asset, out of 100.
	Scores     map[string]float64 `json:"scores"`
	Quality    float64            `json:"quality"`
	Status     Status             `json:"status" enums:"compliant,warning,noncompliant"`
	IssueCount int                `json:"issue_count"`
	// Issues is filled for the last successful run only.
	Issues []Issue `json:"issues,omitempty"`

	// Stub marks a placeholder created by lineage: it is counted apart and never stored or scored.
	Stub     bool                   `json:"stub,omitempty"`
	Sections map[string]SectionStat `json:"-"`
	// Dimensions are the checks the asset meets, in the order of dimensionOrder; they are written
	// to the asset with its score and are not stored with the results, so only an evaluation of
	// an asset carries them.
	Dimensions []string `json:"dimensions,omitempty"`
} // @name QualityAssetResult

// Auditor scores assets against the effective metamodel with the settings of one run.
type Auditor struct {
	registry *metamodel.Registry
	settings Settings
	today    string
	day      time.Time
	rules    []*compiledRule
	fields   []metamodel.Field
	byID     map[string]metamodel.Field
}

// AuditorOption adjusts an Auditor.
type AuditorOption func(*Auditor, *[]CustomRule)

// WithCustomRules adds the rules people wrote in the interface to the ones the profile declares.
func WithCustomRules(rules []CustomRule) AuditorOption {
	return func(_ *Auditor, custom *[]CustomRule) { *custom = rules }
}

func NewAuditor(registry *metamodel.Registry, settings Settings, now time.Time, options ...AuditorOption) *Auditor {
	all := registry.Fields("asset")
	day := now.UTC().Truncate(24 * time.Hour)
	a := &Auditor{
		registry: registry,
		settings: settings,
		today:    day.Format(time.DateOnly),
		day:      day,
		byID:     make(map[string]metamodel.Field, len(all)),
	}
	var custom []CustomRule
	for _, option := range options {
		option(a, &custom)
	}
	a.rules = activeRules(registry, settings, custom)
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
	// A finding of a declared rule (profile or custom) carries its rule and severity; the others
	// are the built-in ones, judged by their code and the settings.
	dsl      bool
	rule     RuleID
	severity Severity
}

func ruleOf(code string) RuleID {
	switch code {
	case "required":
		return RuleRequired
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

// linkFindings are the built-in judgement of the external links, which are not a profile field.
func (a *Auditor) linkFindings(links []asset.ExternalLink) []finding {
	var out []finding
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
	judged := map[string]int{metamodel.DimensionCompleteness: total, metamodel.DimensionValidity: filled}
	held := map[string]int{metamodel.DimensionCompleteness: filled, metamodel.DimensionValidity: valid}
	if !as.IsStub {
		findings = append(findings, a.linkFindings(as.ExternalLinks)...)
		for _, rule := range a.rules {
			found, n, ok := rule.apply(values, a.day)
			findings = append(findings, found...)
			judged[rule.dimension] += n
			held[rule.dimension] += ok
		}
	}

	hasError := false
	for _, found := range findings {
		id, severity := found.rule, found.severity
		if !found.dsl {
			id = ruleOf(found.code)
			rule := a.settings.Rules[id]
			if !rule.Enabled {
				continue
			}
			severity = rule.Severity
		}
		hasError = hasError || severity == SeverityError
		result.Issues = append(result.Issues, Issue{
			FieldID: found.fieldID, Code: found.code, RuleID: id, Severity: severity,
			Section: a.byID[found.fieldID].Presentation.Section, Item: found.item,
		})
	}
	result.IssueCount = len(result.Issues)
	if !as.IsStub {
		result.Dimensions = a.dimensions(as, values, findings, hasPages || ingestedBody(as))
	}

	result.Scores = map[string]float64{}
	for _, dimension := range metamodel.QualityDimensions {
		switch {
		case judged[dimension] > 0:
			result.Scores[dimension] = round(float64(held[dimension]) / float64(judged[dimension]) * 100)
		case dimension == metamodel.DimensionCompleteness:
			result.Scores[dimension] = 100
		case dimension == metamodel.DimensionValidity && anyRequired:
			result.Scores[dimension] = 0
		case dimension == metamodel.DimensionValidity:
			result.Scores[dimension] = 100
		}
	}
	quality := a.settings.Weights.mix(result.Scores)
	result.Quality = round(quality)
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
		if found.dsl {
			continue
		}
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
