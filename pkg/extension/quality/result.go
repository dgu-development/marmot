package quality

import "time"

type Status string

const (
	StatusCompliant    Status = "compliant"
	StatusWarning      Status = "warning"
	StatusNoncompliant Status = "noncompliant"
)

// UnassignedDomain is the key of the assets that belong to no domain.
const UnassignedDomain = "unassigned"

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
	// Dimensions are the checks the asset meets, in the order of the audit's order; they are written
	// to the asset with its score and are not stored with the results, so only an evaluation of
	// an asset carries them.
	Dimensions []string `json:"dimensions,omitempty"`
} // @name QualityAssetResult

// ScoreCounts is how a batch of writes went. A conflict is an asset someone edited after the audit
// read it: the next run scores it, so it is not an error.
type ScoreCounts struct{ Written, Conflicts, Failed int }

func (c *ScoreCounts) Add(other ScoreCounts) {
	c.Written += other.Written
	c.Conflicts += other.Conflicts
	c.Failed += other.Failed
}

// RuleSource says where a rule comes from, and so who may change it.
type RuleSource string

const (
	// SourceBuiltin rules judge what the metamodel itself defines; only their severity and whether
	// they apply are set, in the settings.
	SourceBuiltin RuleSource = "builtin"
	// SourceProfile rules are declared in the metamodel profile, which ships with the platform and
	// is read-only at runtime; their severity and whether they apply are set in the settings.
	SourceProfile RuleSource = "profile"
	// SourceCustom rules are written in the interface and kept in the database.
	SourceCustom RuleSource = "custom"
)

// CustomRule is a rule written in the interface. It is kept in the database, versioned for
// compare-and-set like the settings, and evaluated like a rule of the profile.
type CustomRule struct {
	Rule
	Enabled   bool      `json:"enabled"`
	Version   int64     `json:"version"`
	CreatedBy string    `json:"created_by,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
} // @name QualityCustomRule

// RuleInfo is a rule as the interface lists it, wherever it comes from.
type RuleInfo struct {
	ID     RuleID     `json:"id"`
	Source RuleSource `json:"source" enums:"builtin,profile,custom"`
	// Name and Description are literal text, of a custom rule; LabelKey and DescriptionKey resolve
	// through the profile's messages, for a profile rule; a built-in rule has neither and the
	// client names it by its id.
	Name           string `json:"name,omitempty"`
	Description    string `json:"description,omitempty"`
	LabelKey       string `json:"label_key,omitempty"`
	DescriptionKey string `json:"description_key,omitempty"`
	Code           string `json:"code,omitempty"`
	// Dimension is the quality dimension the rule feeds.
	Dimension string   `json:"dimension" enums:"completeness,validity,consistency,timeliness"`
	Severity  Severity `json:"severity" enums:"error,warning"`
	Enabled   bool     `json:"enabled"`
	// Definition is what the rule checks, for the rules that declare it.
	Definition *Rule `json:"definition,omitempty"`
	// Version, for a custom rule, is what to send back with If-Match.
	Version int64 `json:"version,omitempty"`
	// Problem says why a custom rule is not applied: it names a field the profile no longer has.
	Problem string `json:"problem,omitempty"`
} // @name QualityRuleInfo
