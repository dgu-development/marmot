package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SetAssetBadgePaths names the metadata.* bindings of the asset fields shown
// as badges; asset results carry only those values.
func (r *PostgresRepository) SetAssetBadgePaths(storages []string) {
	r.assetBadgePaths = nil
	for _, storage := range storages {
		if path, ok := strings.CutPrefix(storage, "metadata."); ok {
			r.assetBadgePaths = append(r.assetBadgePaths, path)
		}
	}
}

// attachEntityMetadata adds each glossary term's and data product's own
// metadata as Metadata["metadata"], so clients can show its profile fields
// (such as a badge) in results. Assets only carry their badge values: their
// metadata can be large, and a page of results would carry all of it.
func (r *PostgresRepository) attachEntityMetadata(ctx context.Context, results []*Result) error {
	if err := r.attachAssetBadges(ctx, results); err != nil {
		return err
	}
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

func (r *PostgresRepository) attachAssetBadges(ctx context.Context, results []*Result) error {
	if len(r.assetBadgePaths) == 0 {
		return nil
	}
	var ids []string
	byID := map[string]*Result{}
	for _, res := range results {
		if res.Type == ResultTypeAsset {
			ids = append(ids, res.ID)
			byID[res.ID] = res
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := r.db.Query(ctx, `SELECT si.entity_id, p.path, si.metadata #> string_to_array(p.path, '.')
		FROM search_index si CROSS JOIN unnest($2::text[]) AS p(path)
		WHERE si.type = 'asset' AND si.entity_id = ANY($1)
			AND jsonb_typeof(si.metadata #> string_to_array(p.path, '.')) = 'string'`, ids, r.assetBadgePaths)
	if err != nil {
		return fmt.Errorf("loading asset badges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, path string
		var value []byte
		if err := rows.Scan(&id, &path, &value); err != nil {
			return fmt.Errorf("scanning asset badges: %w", err)
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return fmt.Errorf("decoding badge of asset %s: %w", id, err)
		}
		res := byID[id]
		if res.Metadata == nil {
			res.Metadata = map[string]interface{}{}
		}
		metadata, _ := res.Metadata["metadata"].(map[string]interface{})
		if metadata == nil {
			metadata = map[string]interface{}{}
			res.Metadata["metadata"] = metadata
		}
		parts := strings.Split(path, ".")
		object := metadata
		for _, part := range parts[:len(parts)-1] {
			child, _ := object[part].(map[string]interface{})
			if child == nil {
				child = map[string]interface{}{}
				object[part] = child
			}
			object = child
		}
		object[parts[len(parts)-1]] = text
	}
	return rows.Err()
}
