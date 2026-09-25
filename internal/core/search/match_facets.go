package search

import (
	"context"
	"fmt"

	"github.com/marmotdata/marmot/internal/query"
)

// maxCountedMatches caps how many matches a search counts, as full-text
// search already caps its candidates; a total at the cap is a lower bound.
const maxCountedMatches = 1000

// matchFacets counts a search's matches by kind and asset type with the same
// query that finds them. Provider, tag and metadata facets are left out: they
// need an unnest over every match.
func (r *PostgresRepository) matchFacets(ctx context.Context, searchQuery string, filter Filter, parsedQuery *query.Query, facets *Facets) (*Facets, int, error) {
	capped := filter
	capped.Limit = maxCountedMatches
	capped.Offset = 0
	inner, params := r.buildOptimizedSearchQuery(searchQuery, capped, parsedQuery)

	rows, err := r.db.Query(ctx, `SELECT type, asset_type, COUNT(*) FROM (`+inner+`) matches GROUP BY type, asset_type`, params...)
	if err != nil {
		return nil, 0, fmt.Errorf("counting search matches: %w", err)
	}
	defer rows.Close()

	total := 0
	assetTypes := map[string]int{}
	for rows.Next() {
		var kind string
		var assetType *string
		var count int
		if err := rows.Scan(&kind, &assetType, &count); err != nil {
			return nil, 0, fmt.Errorf("scanning search match counts: %w", err)
		}
		facets.Types[ResultType(kind)] += count
		total += count
		if assetType != nil && kind == string(ResultTypeAsset) {
			assetTypes[*assetType] += count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for value, count := range assetTypes {
		facets.AssetTypes = append(facets.AssetTypes, FacetValue{Value: value, Count: count})
	}
	sortFacetValues(facets.AssetTypes)
	if len(facets.AssetTypes) > maxFacetResults {
		facets.AssetTypes = facets.AssetTypes[:maxFacetResults]
	}
	return facets, total, nil
}
