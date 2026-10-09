package asset

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/marmotdata/marmot/internal/core/metamodel"
)

// AssetRef identifies an asset at the other end of an asset-control field.
type AssetRef struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Providers []string `json:"providers"`
	MRN       *string  `json:"mrn,omitempty"`
} // @name AssetRef

// AssetReferences lists the assets whose Field points at an asset.
type AssetReferences struct {
	Field  string     `json:"field"`
	Assets []AssetRef `json:"assets"`
} // @name AssetReferences

// LinkFields returns the profile's asset fields that hold asset IDs.
func LinkFields(registry *metamodel.Registry) []metamodel.Field {
	if registry == nil || !registry.Enabled() {
		return nil
	}
	var fields []metamodel.Field
	for _, f := range registry.Fields("asset") {
		if f.Presentation.Control == metamodel.ControlAsset && strings.HasPrefix(f.Storage, "metadata.") {
			fields = append(fields, f)
		}
	}
	return fields
}

// LinkIDs reads the asset IDs an asset-control field holds, as a string or a list.
func LinkIDs(value any) []string {
	switch v := value.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		ids := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				ids = append(ids, s)
			}
		}
		return ids
	case []string:
		return slices.DeleteFunc(slices.Clone(v), func(s string) bool { return s == "" })
	}
	return nil
}

// mrnPrefix marks a link written by something that only knows the target's MRN, such as a
// connector: IDs are minted here, so a source cannot name them.
const mrnPrefix = "mrn://"

// HasLinkMRNs reports whether metadata holds an MRN anywhere a link could be.
func HasLinkMRNs(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.HasPrefix(v, mrnPrefix)
	case []any:
		return slices.ContainsFunc(v, HasLinkMRNs)
	case []string:
		return slices.ContainsFunc(v, func(item string) bool { return strings.HasPrefix(item, mrnPrefix) })
	case map[string]any:
		for _, item := range v {
			if HasLinkMRNs(item) {
				return true
			}
		}
	}
	return false
}

// resolveLinkMRNs replaces, in the asset-control fields, each MRN with the ID of the asset it
// names, on a copy of the metadata. An MRN that names nothing is left out; with deferred, its whole field is, so that a run
// which creates both ends can fill the field in one go on its second pass instead of leaving a
// partial value that would then count as curated.
func (s *service) resolveLinkMRNs(ctx context.Context, asset *Asset, deferred bool) error {
	if !HasLinkMRNs(asset.Metadata) {
		return nil
	}
	// The caller keeps its map as it sent it: a run sends the same metadata again on its second pass.
	metadata, err := cloneMetadata(asset.Metadata)
	if err != nil {
		return err
	}
	asset.Metadata = metadata
	for _, f := range LinkFields(s.registry()) {
		value, present := metamodel.ValueAt(metadata, f.Storage)
		if !present || !HasLinkMRNs(value) {
			continue
		}
		raw := LinkIDs(value)
		ids := make([]any, 0, len(raw))
		missing := false
		for _, item := range raw {
			if !strings.HasPrefix(item, mrnPrefix) {
				ids = append(ids, item)
				continue
			}
			target, err := s.repo.GetByMRN(ctx, item)
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrAssetNotFound) {
				missing = true
				continue
			}
			if err != nil {
				return fmt.Errorf("resolving linked asset %s: %w", item, err)
			}
			ids = append(ids, target.ID)
		}
		var resolved any = ids
		if _, single := value.(string); single && len(ids) > 0 {
			resolved = ids[0]
		}
		if len(ids) == 0 || (missing && deferred) {
			resolved = nil
		}
		parts := strings.Split(strings.TrimPrefix(f.Storage, "metadata."), ".")
		if err := setMetadataValue(metadata, parts, resolved); err != nil {
			return fmt.Errorf("field %q: %w", f.ID, err)
		}
	}
	return nil
}

// checkLinks rejects asset-control values naming the asset itself, or adding an asset that does
// not exist or whose asset_type the field's targetAssetTypes leaves out. Links the asset already had are kept even once their target is deleted, so an
// unrelated edit or a re-ingest never fails on them.
func (s *service) checkLinks(ctx context.Context, self string, metadata, previous map[string]interface{}) error {
	var violations []metamodel.Violation
	for _, f := range LinkFields(s.registry()) {
		value, present := metamodel.ValueAt(metadata, f.Storage)
		if !present {
			continue
		}
		ids := LinkIDs(value)
		if self != "" && slices.Contains(ids, self) {
			violations = append(violations, metamodel.Violation{Field: f.ID, Code: "self_reference"})
			continue
		}
		var kept []string
		if old, ok := metamodel.ValueAt(previous, f.Storage); ok {
			kept = LinkIDs(old)
		}
		added := slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return slices.Contains(kept, id) })
		slices.Sort(added)
		added = slices.Compact(added)
		valid := make([]string, 0, len(added))
		for _, id := range added {
			if _, err := uuid.Parse(id); err == nil {
				valid = append(valid, id)
			}
		}
		found, err := s.repo.RefsByID(ctx, valid)
		if err != nil {
			return fmt.Errorf("checking linked assets: %w", err)
		}
		if len(found) != len(added) {
			violations = append(violations, metamodel.Violation{Field: f.ID, Code: "asset_not_found"})
			continue
		}
		allowed, err := s.targetsAllowed(ctx, f, valid)
		if err != nil {
			return err
		}
		if !allowed {
			violations = append(violations, metamodel.Violation{Field: f.ID, Code: "target_type"})
			continue
		}
		if f.Validation.Acyclic && self != "" {
			loops, err := s.leadsBack(ctx, f, valid, self)
			if err != nil {
				return err
			}
			if loops {
				violations = append(violations, metamodel.Violation{Field: f.ID, Code: "cycle"})
			}
		}
	}
	if len(violations) > 0 {
		return &metamodel.ValidationError{Fields: violations}
	}
	return nil
}

// maxLinkDepth bounds the walk of an acyclic field; a hierarchy deeper than this is refused as if
// it looped rather than read without end.
const maxLinkDepth = 256

// leadsBack reports whether following the field from any of ids reaches self.
func (s *service) leadsBack(ctx context.Context, f metamodel.Field, ids []string, self string) (bool, error) {
	seen := map[string]bool{}
	queue := slices.Clone(ids)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == self || len(seen) >= maxLinkDepth {
			return true, nil
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		linked, err := s.repo.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("walking %s: %w", f.ID, err)
		}
		if value, ok := metamodel.ValueAt(linked.Metadata, f.Storage); ok {
			queue = append(queue, LinkIDs(value)...)
		}
	}
	return false, nil
}

// targetsAllowed reports whether every asset in ids has an asset_type the field accepts.
func (s *service) targetsAllowed(ctx context.Context, f metamodel.Field, ids []string) (bool, error) {
	if len(f.Presentation.TargetAssetTypes) == 0 {
		return true, nil
	}
	typeField, ok := s.registry().Field(metamodel.AssetTypeField)
	if !ok {
		return false, nil
	}
	for _, id := range ids {
		target, err := s.repo.Get(ctx, id)
		if err != nil {
			return false, fmt.Errorf("checking the type of linked asset %s: %w", id, err)
		}
		value, _ := metamodel.ValueAt(target.Metadata, typeField.Storage)
		if assetType, _ := value.(string); !slices.Contains(f.Presentation.TargetAssetTypes, assetType) {
			return false, nil
		}
	}
	return true, nil
}

// References returns, per asset-control field, the assets pointing at id.
func (s *service) References(ctx context.Context, id string) ([]AssetReferences, error) {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return nil, err
	}
	out := []AssetReferences{}
	for _, f := range LinkFields(s.registry()) {
		path := strings.Split(strings.TrimPrefix(f.Storage, "metadata."), ".")
		assets, err := s.repo.ReferencedBy(ctx, path, id)
		if err != nil {
			return nil, fmt.Errorf("finding references to %s: %w", id, err)
		}
		if len(assets) > 0 {
			out = append(out, AssetReferences{Field: f.ID, Assets: assets})
		}
	}
	return out, nil
}

// RefsByID resolves asset IDs to the assets that exist.
func (s *service) RefsByID(ctx context.Context, ids []string) ([]AssetRef, error) {
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := uuid.Parse(id); err == nil {
			valid = append(valid, id)
		}
	}
	return s.repo.RefsByID(ctx, valid)
}

func (r *PostgresRepository) RefsByID(ctx context.Context, ids []string) ([]AssetRef, error) {
	if len(ids) == 0 {
		return []AssetRef{}, nil
	}
	return r.refs(ctx, `SELECT id::text, COALESCE(name, mrn, ''), type, providers, mrn FROM assets
		WHERE id = ANY($1::text[]) ORDER BY name`, ids)
}

func (r *PostgresRepository) ReferencedBy(ctx context.Context, path []string, id string) ([]AssetRef, error) {
	return r.refs(ctx, `SELECT id::text, COALESCE(name, mrn, ''), type, providers, mrn FROM assets
		WHERE is_stub = FALSE
		  AND (metadata #> $1::text[] @> jsonb_build_array($2::text) OR metadata #> $1::text[] = to_jsonb($2::text))
		ORDER BY name`, path, id)
}

func (r *PostgresRepository) refs(ctx context.Context, query string, args ...any) ([]AssetRef, error) {
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []AssetRef{}
	for rows.Next() {
		var ref AssetRef
		if err := rows.Scan(&ref.ID, &ref.Name, &ref.Type, &ref.Providers, &ref.MRN); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
