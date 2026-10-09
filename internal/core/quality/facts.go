package quality

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marmotdata/marmot/internal/core/asset"
	extquality "github.com/marmotdata/marmot/pkg/extension/quality"
)

var ErrMetamodelOff = extquality.ErrNoProfile

// AssetSource reads the catalog by key, a batch at a time.
type AssetSource interface {
	ListAfter(ctx context.Context, afterID string, limit int) ([]*asset.Asset, error)
	// ListByIDs returns the assets that exist among the ids.
	ListByIDs(ctx context.Context, ids []string) ([]*asset.Asset, error)
	Count(ctx context.Context) (int, error)
}

type postgresFacts struct{ db *pgxpool.Pool }

// DocumentedAssets says which of the assets, by MRN, have a documentation page of their own or the
// older per-source documentation.
func (r *postgresFacts) DocumentedAssets(ctx context.Context, mrns []string) (map[string]bool, error) {
	out := make(map[string]bool, len(mrns))
	if len(mrns) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT entity_id FROM doc_pages WHERE entity_type = 'asset' AND entity_id = ANY($1)
		UNION
		SELECT mrn FROM documentation WHERE mrn = ANY($1) AND btrim(content) <> ''`, mrns)
	if err != nil {
		return nil, fmt.Errorf("reading documentation: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mrn string
		if err := rows.Scan(&mrn); err != nil {
			return nil, err
		}
		out[mrn] = true
	}
	return out, rows.Err()
}

// unassignedDomainID is the row of the domain that holds what has no explicit domain.
const unassignedDomainID = "00000000-0000-4000-8000-000000000001"

// AssetDomains maps each asset to its domain; an asset with no membership row, or in the
// Unassigned domain, is left out so the caller keeps its "unassigned" default.
func (r *postgresFacts) AssetDomains(ctx context.Context, assetIDs []string) (map[string]string, error) {
	rows, err := r.db.Query(ctx, `SELECT asset_id, domain_id::text FROM asset_domains WHERE asset_id = ANY($1)`, assetIDs)
	if err != nil {
		return nil, fmt.Errorf("reading asset domains: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string, len(assetIDs))
	for rows.Next() {
		var assetID, domainID string
		if err := rows.Scan(&assetID, &domainID); err != nil {
			return nil, err
		}
		if domainID == unassignedDomainID {
			continue
		}
		out[assetID] = domainID
	}
	return out, rows.Err()
}
