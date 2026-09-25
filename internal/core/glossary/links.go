package glossary

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/marmotdata/marmot/internal/core/metamodel"
)

// TermRef identifies a term at the other end of a glossary_term field.
type TermRef struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Definition string `json:"definition"`
} // @name GlossaryTermRef

// TermReferences lists the live terms whose Field points at a term.
type TermReferences struct {
	Field string    `json:"field"`
	Terms []TermRef `json:"terms"`
} // @name GlossaryTermReferences

// LinkFields returns the profile's glossary_term fields that hold term IDs.
func LinkFields(registry *metamodel.Registry) []metamodel.Field {
	if registry == nil || !registry.Enabled() {
		return nil
	}
	var fields []metamodel.Field
	for _, f := range registry.Fields(metamodelKind) {
		if f.Presentation.Control == metamodel.ControlGlossaryTerm && strings.HasPrefix(f.Storage, "metadata.") {
			fields = append(fields, f)
		}
	}
	return fields
}

// LinkIDs reads the term IDs a glossary_term field holds, as a string or a list.
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

// checkTermLinks rejects glossary_term values naming self, or adding a term
// that is not live. Links the term already had are kept even once their
// target is deleted, so an unrelated edit or a re-import never fails on them.
func (s *service) checkTermLinks(ctx context.Context, self string, metadata, previous map[string]interface{}) error {
	var violations []metamodel.Violation
	for _, f := range LinkFields(s.metamodel) {
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
		added := uniq(slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return slices.Contains(kept, id) }))
		valid := make([]string, 0, len(added))
		for _, id := range added {
			if _, err := uuid.Parse(id); err == nil {
				valid = append(valid, id)
			}
		}
		found, err := s.repo.RefsByID(ctx, valid)
		if err != nil {
			return fmt.Errorf("checking linked terms: %w", err)
		}
		if len(found) != len(added) {
			violations = append(violations, metamodel.Violation{Field: f.ID, Code: "term_not_found"})
		}
	}
	if len(violations) > 0 {
		return &metamodel.ValidationError{Fields: violations}
	}
	return nil
}

func uniq(ids []string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}

// References returns, per glossary_term field, the live terms pointing at id.
func (s *service) References(ctx context.Context, id string) ([]TermReferences, error) {
	if _, err := s.load(ctx, id); err != nil {
		return nil, err
	}
	out := []TermReferences{}
	for _, f := range LinkFields(s.metamodel) {
		path := strings.Split(strings.TrimPrefix(f.Storage, "metadata."), ".")
		terms, err := s.repo.ReferencedBy(ctx, path, id)
		if err != nil {
			return nil, fmt.Errorf("finding references to %s: %w", id, err)
		}
		if len(terms) > 0 {
			out = append(out, TermReferences{Field: f.ID, Terms: terms})
		}
	}
	return out, nil
}

// RefsByID resolves term IDs to the live terms that have them.
func (s *service) RefsByID(ctx context.Context, ids []string) ([]TermRef, error) {
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := uuid.Parse(id); err == nil {
			valid = append(valid, id)
		}
	}
	return s.repo.RefsByID(ctx, valid)
}

func (r *PostgresRepository) RefsByID(ctx context.Context, ids []string) ([]TermRef, error) {
	if len(ids) == 0 {
		return []TermRef{}, nil
	}
	return r.refs(ctx, `SELECT id::text, name, COALESCE(user_definition, definition) FROM glossary_terms
		WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL ORDER BY name`, ids)
}

func (r *PostgresRepository) ReferencedBy(ctx context.Context, path []string, id string) ([]TermRef, error) {
	return r.refs(ctx, `SELECT id::text, name, COALESCE(user_definition, definition) FROM glossary_terms
		WHERE deleted_at IS NULL
		  AND (metadata #> $1::text[] @> jsonb_build_array($2::text) OR metadata #> $1::text[] = to_jsonb($2::text))
		ORDER BY name`, path, id)
}

func (r *PostgresRepository) refs(ctx context.Context, query string, args ...any) ([]TermRef, error) {
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []TermRef{}
	for rows.Next() {
		var ref TermRef
		if err := rows.Scan(&ref.ID, &ref.Name, &ref.Definition); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
