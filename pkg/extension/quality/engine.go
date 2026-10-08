package quality

import (
	"context"
	"errors"
	"time"
)

// ErrNoProfile answers an audit on a server without a metadata profile: there is nothing to
// judge against.
var ErrNoProfile = errors.New("the metamodel profile is not loaded")

// Profile identifies the metadata profile the audit judges with, for the record of a run.
type Profile struct {
	ID      string
	Version int
	Hash    string
	// Enabled is false on a server without a profile.
	Enabled bool
}

// Audit asks the engine to judge a batch of assets: the ones with IDs, or up to Limit after an
// ID in ID order, which is how a run walks the catalog.
type Audit struct {
	Settings Settings
	Custom   []CustomRule
	// Now is the day date rules compare with; one run uses the same for every batch.
	Now   time.Time
	After string
	Limit int
	IDs   []string
	// Publish writes the score of each asset on it, as the platform, when it changed.
	Publish bool
}

// Page is a judged batch. Results follow the order the assets were read in and include the
// stubs, which a run counts apart and does not store.
type Page struct {
	Results []AssetResult
	// Last is the ID to continue after; empty when the batch was empty.
	Last   string
	Scores ScoreCounts
}

// Engine is the evaluating half of the audit, which stays in the server because it is the
// profile's criterion applied to the catalog. Whoever runs and records audits calls it.
type Engine interface {
	Profile() Profile
	// ProfileRules are the rules the profile declares, besides the built-in ones.
	ProfileRules() []Rule
	// ValidateRule reports every problem of a rule a person wrote, as a *RuleError.
	ValidateRule(rule Rule) error
	CountAssets(ctx context.Context) (int, error)
	Audit(ctx context.Context, audit Audit) (Page, error)
}
