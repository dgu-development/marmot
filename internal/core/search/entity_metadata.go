package search

import (
	"context"
	"encoding/json"
	"fmt"
)

// attachEntityMetadata adds each glossary term's and data product's own
// metadata as Metadata["metadata"], so clients can show its profile fields
// (such as a badge) in results. Assets are left out: their metadata can be
// large, and a page of results would carry all of it.
func (r *PostgresRepository) attachEntityMetadata(ctx context.Context, results []*Result) error {
	var ids []string
	byKey := map[string]*Result{}
	for _, res := range results {
		if res.Type != ResultTypeGlossary && res.Type != ResultTypeDataProduct {
			continue
		}
		ids = append(ids, res.ID)
		byKey[string(res.Type)+"/"+res.ID] = res
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := r.db.Query(ctx, `SELECT type, entity_id, metadata FROM search_index
		WHERE type IN ('glossary', 'data_product') AND entity_id = ANY($1)`, ids)
	if err != nil {
		return fmt.Errorf("loading result metadata: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id string
		var raw []byte
		if err := rows.Scan(&kind, &id, &raw); err != nil {
			return fmt.Errorf("scanning result metadata: %w", err)
		}
		res, ok := byKey[kind+"/"+id]
		if !ok || len(raw) == 0 {
			continue
		}
		var metadata map[string]interface{}
		if err := json.Unmarshal(raw, &metadata); err != nil {
			return fmt.Errorf("decoding metadata of %s %s: %w", kind, id, err)
		}
		if res.Metadata == nil {
			res.Metadata = map[string]interface{}{}
		}
		res.Metadata["metadata"] = metadata
	}
	return rows.Err()
}
