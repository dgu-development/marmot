package quality

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	extquality "github.com/marmotdata/marmot/pkg/extension/quality"
)

// AssetFacts is what the audit reads about a batch of assets besides the assets themselves.
type AssetFacts interface {
	// AssetDomains maps each asset to its domain; one with none is left out.
	AssetDomains(ctx context.Context, assetIDs []string) (map[string]string, error)
	// DocumentedAssets says which assets, by MRN, have documentation of their own.
	DocumentedAssets(ctx context.Context, mrns []string) (map[string]bool, error)
}

// Engine implements extquality.Engine on the catalog: it is what the host lends an extension
// that runs and records audits.
type Engine struct {
	registry *metamodel.Registry
	assets   AssetSource
	facts    AssetFacts
	writer   ScoreWriter
}

// NewEngine builds the engine. A nil writer makes Publish a no-op.
func NewEngine(registry *metamodel.Registry, assets AssetSource, facts AssetFacts, writer ScoreWriter) *Engine {
	return &Engine{registry: registry, assets: assets, facts: facts, writer: writer}
}

// NewAssetFacts reads the facts from the catalog's tables.
func NewAssetFacts(db *pgxpool.Pool) AssetFacts { return &postgresFacts{db: db} }

func (e *Engine) Profile() extquality.Profile {
	schema := e.registry.Schema()
	return extquality.Profile{ID: schema.ID, Version: schema.Version, Hash: schema.Hash, Enabled: e.registry.Enabled()}
}

func (e *Engine) ProfileRules() []extquality.Rule { return e.registry.QualityRules() }

func (e *Engine) ValidateRule(rule extquality.Rule) error {
	if !e.registry.Enabled() {
		return ErrMetamodelOff
	}
	return metamodel.ValidateQualityRule(rule, e.registry.QualityRuleFields(), true)
}

func (e *Engine) CountAssets(ctx context.Context) (int, error) { return e.assets.Count(ctx) }

func (e *Engine) Audit(ctx context.Context, in extquality.Audit) (extquality.Page, error) {
	var page extquality.Page
	if !e.registry.Enabled() {
		return page, ErrMetamodelOff
	}
	var batch []*asset.Asset
	var err error
	if in.IDs != nil {
		batch, err = e.assets.ListByIDs(ctx, in.IDs)
	} else {
		batch, err = e.assets.ListAfter(ctx, in.After, in.Limit)
	}
	if err != nil {
		return page, fmt.Errorf("reading assets: %w", err)
	}
	if len(batch) == 0 {
		return page, nil
	}
	ids := make([]string, len(batch))
	mrns := make([]string, 0, len(batch))
	for i, a := range batch {
		ids[i] = a.ID
		if a.MRN != nil {
			mrns = append(mrns, *a.MRN)
		}
	}
	domains, err := e.facts.AssetDomains(ctx, ids)
	if err != nil {
		return page, err
	}
	documented, err := e.facts.DocumentedAssets(ctx, mrns)
	if err != nil {
		return page, err
	}
	auditor := NewAuditor(e.registry, in.Settings, in.Now, WithCustomRules(in.Custom))
	page.Results = make([]AssetResult, len(batch))
	for i, a := range batch {
		result := auditor.AuditWithDocs(a, a.MRN != nil && documented[*a.MRN])
		if domain, ok := domains[a.ID]; ok {
			result.DomainID = domain
		}
		page.Results[i] = result
	}
	page.Last = batch[len(batch)-1].ID
	if in.Publish && e.writer != nil {
		page.Scores, err = publish(ctx, e.writer, auditor, batch, page.Results)
	}
	return page, err
}
