// Package quality is the fork-only metadata quality audit: versioned settings,
// manual runs with their results, and the history kept in tables of their own.
// See the ADR-007 of the distribution and web/docs/docs/Configure/quality.md.
package quality

import extquality "github.com/marmotdata/marmot/pkg/extension/quality"

// The data of the audit lives in pkg/extension/quality, which extensions share with the server.
type (
	RuleID          = extquality.RuleID
	Severity        = extquality.Severity
	RuleSetting     = extquality.RuleSetting
	Weights         = extquality.Weights
	Thresholds      = extquality.Thresholds
	Retention       = extquality.Retention
	Settings        = extquality.Settings
	Stored          = extquality.Stored
	FieldError      = extquality.FieldError
	ValidationError = extquality.ValidationError
	Status          = extquality.Status
	Issue           = extquality.Issue
	SectionStat     = extquality.SectionStat
	AssetResult     = extquality.AssetResult
	ScoreCounts     = extquality.ScoreCounts
	RuleSource      = extquality.RuleSource
	CustomRule      = extquality.CustomRule
	RuleInfo        = extquality.RuleInfo
	FieldCount      = extquality.FieldCount
	GroupStat       = extquality.GroupStat
	Summary         = extquality.Summary
	Aggregator      = extquality.Aggregator
)

const (
	RuleRequired            = extquality.RuleRequired
	RuleValidation          = extquality.RuleValidation
	RuleExternalLinkInvalid = extquality.RuleExternalLinkInvalid
	RuleExternalLinkEmpty   = extquality.RuleExternalLinkEmpty
	SeverityError           = extquality.SeverityError
	SeverityWarning         = extquality.SeverityWarning
	StatusCompliant         = extquality.StatusCompliant
	StatusWarning           = extquality.StatusWarning
	StatusNoncompliant      = extquality.StatusNoncompliant
	UnassignedDomain        = extquality.UnassignedDomain
	SourceBuiltin           = extquality.SourceBuiltin
	SourceProfile           = extquality.SourceProfile
	SourceCustom            = extquality.SourceCustom
	MinBatchSize            = extquality.MinBatchSize
	MaxBatchSize            = extquality.MaxBatchSize
	MinRunSeconds           = extquality.MinRunSeconds
	MaxRunSeconds           = extquality.MaxRunSeconds
	MaxRetentionDays        = extquality.MaxRetentionDays
)

var (
	Rules           = extquality.Rules
	DefaultSettings = extquality.DefaultSettings
	ParseSchedule   = extquality.ParseSchedule
	NewAggregator   = extquality.NewAggregator
)
