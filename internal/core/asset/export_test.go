package asset

// The tests that need a database live in asset_test: pgtest reaches this package through the
// plugin registry, so importing it from here would close a cycle.
var (
	MustLoadProfile = mustLoadProfile
	ValidCreate     = validCreate
)
