package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrVersionNotFound = errors.New("ontology version not found")

// OntologyVersion is a published state of a domain's ontology. Snapshot is only
// filled when a single version is read.
type OntologyVersion struct {
	ID              string          `json:"id"`
	DomainID        string          `json:"domain_id"`
	Version         int             `json:"version"`
	Note            string          `json:"note"`
	BeforeRestoreOf *int            `json:"before_restore_of,omitempty"`
	CreatedBy       *string         `json:"created_by,omitempty"`
	CreatedByName   *string         `json:"created_by_name,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	Terms           int             `json:"terms"`
	Snapshot        json.RawMessage `json:"snapshot,omitempty" swaggertype:"object"`
} // @name OntologyVersion

// OntologyVersions keeps and restores the versions of domain ontologies.
type OntologyVersions struct {
	db *pgxpool.Pool
}

func NewOntologyVersions(db *pgxpool.Pool) *OntologyVersions {
	return &OntologyVersions{db: db}
}

const ontologyVersionColumns = `v.id, v.domain_id, v.version, v.note, v.before_restore_of, v.created_by,
	(SELECT u.name FROM users u WHERE u.id::text = v.created_by), v.created_at, jsonb_array_length(v.snapshot->'terms')`

func scanOntologyVersion(row pgx.Row, extra ...any) (*OntologyVersion, error) {
	var v OntologyVersion
	dest := append([]any{&v.ID, &v.DomainID, &v.Version, &v.Note, &v.BeforeRestoreOf, &v.CreatedBy, &v.CreatedByName, &v.CreatedAt, &v.Terms}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	return &v, nil
}

// List returns the versions of a domain's ontology, newest first, without their snapshots.
func (o *OntologyVersions) List(ctx context.Context, domainID string) ([]*OntologyVersion, error) {
	rows, err := o.db.Query(ctx, `SELECT `+ontologyVersionColumns+` FROM ontology_versions v WHERE v.domain_id = $1 ORDER BY v.version DESC`, domainID)
	if err != nil {
		return nil, fmt.Errorf("listing ontology versions: %w", err)
	}
	defer rows.Close()
	out := []*OntologyVersion{}
	for rows.Next() {
		v, err := scanOntologyVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("reading ontology version: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Get returns one version with its snapshot.
func (o *OntologyVersions) Get(ctx context.Context, domainID string, version int) (*OntologyVersion, error) {
	var snapshot []byte
	v, err := scanOntologyVersion(o.db.QueryRow(ctx, `SELECT `+ontologyVersionColumns+`, v.snapshot FROM ontology_versions v WHERE v.domain_id = $1 AND v.version = $2`, domainID, version), &snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVersionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading ontology version: %w", err)
	}
	v.Snapshot = snapshot
	return v, nil
}

// Publish stores the ontology of the domain as it is now.
func (o *OntologyVersions) Publish(ctx context.Context, domainID, note string, actor *string) (*OntologyVersion, error) {
	tx, err := o.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	v, err := publishOntology(ctx, tx, domainID, note, nil, actor)
	if err != nil {
		return nil, err
	}
	return v, tx.Commit(ctx)
}

func publishOntology(ctx context.Context, tx pgx.Tx, domainID, note string, beforeRestoreOf *int, actor *string) (*OntologyVersion, error) {
	// The row lock orders concurrent publishes and restores of one domain, so version numbers do not collide.
	var locked string
	if err := tx.QueryRow(ctx, `SELECT id FROM domains WHERE id = $1 FOR UPDATE`, domainID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || pgCode(err) == "22P02" {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("locking domain: %w", err)
	}
	v, err := scanOntologyVersion(tx.QueryRow(ctx, `
		WITH made AS (
			INSERT INTO ontology_versions (domain_id, version, note, before_restore_of, created_by, snapshot)
			SELECT d.id,
				COALESCE((SELECT MAX(version) FROM ontology_versions WHERE domain_id = d.id), 0) + 1,
				$2, $3, $4,
				jsonb_build_object(
					'domain', jsonb_build_object('name', d.name, 'description', d.description, 'ontology', COALESCE(d.metadata->'ontology', '{}'::jsonb)),
					'terms', COALESCE((
						SELECT jsonb_agg(jsonb_build_object(
							'id', t.id, 'name', t.name, 'definition', t.definition, 'user_definition', t.user_definition,
							'description', t.description, 'parent_term_id', t.parent_term_id, 'metadata', t.metadata, 'tags', t.tags
						) ORDER BY t.name, t.id)
						FROM glossary_terms t
						JOIN glossary_term_domains g ON g.glossary_term_id = t.id
						WHERE g.domain_id = d.id AND t.deleted_at IS NULL), '[]'::jsonb))
			FROM domains d WHERE d.id = $1
			RETURNING *
		)
		SELECT `+ontologyVersionColumns+` FROM made v`, domainID, note, beforeRestoreOf, actor))
	if err != nil {
		return nil, fmt.Errorf("publishing ontology version: %w", err)
	}
	return v, nil
}

// Restore puts the domain's glossary terms, and what the domain says about its ontology, back
// as they were in a version. The state it replaces is published first, so a restore can itself
// be undone. Terms created since are soft-deleted; owners and asset links are left as they are.
func (o *OntologyVersions) Restore(ctx context.Context, domainID string, version int, actor *string) (*OntologyVersion, error) {
	tx, err := o.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	saved, err := publishOntology(ctx, tx, domainID, "", &version, actor)
	if err != nil {
		return nil, err
	}
	var snapshot []byte
	if err := tx.QueryRow(ctx, `SELECT snapshot FROM ontology_versions WHERE domain_id = $1 AND version = $2`, domainID, version).Scan(&snapshot); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVersionNotFound
		}
		return nil, fmt.Errorf("reading ontology version: %w", err)
	}

	// The search index follows glossary_terms through its trigger.
	// ponytail: an external search indexer is not told; reindex there if one is ever configured.
	if _, err := tx.Exec(ctx, `
		UPDATE glossary_terms t SET deleted_at = now(), updated_at = now()
		WHERE t.deleted_at IS NULL
		  AND t.id IN (SELECT glossary_term_id FROM glossary_term_domains WHERE domain_id = $1)
		  AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements($2::jsonb->'terms') s WHERE (s->>'id')::uuid = t.id)`, domainID, snapshot); err != nil {
		return nil, fmt.Errorf("removing terms added since the version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE glossary_terms t
		SET name = s.name, definition = s.definition, user_definition = s.user_definition, description = s.description,
			parent_term_id = s.parent_term_id, metadata = COALESCE(s.metadata, '{}'::jsonb), tags = s.tags,
			deleted_at = NULL, updated_at = now()
		FROM jsonb_to_recordset($1::jsonb->'terms') AS s(id uuid, name text, definition text, user_definition text,
			description text, parent_term_id uuid, metadata jsonb, tags text[])
		WHERE t.id = s.id`, snapshot); err != nil {
		return nil, fmt.Errorf("restoring terms: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE glossary_term_domains g SET domain_id = $1
		WHERE g.domain_id <> $1 AND EXISTS (SELECT 1 FROM jsonb_array_elements($2::jsonb->'terms') s WHERE (s->>'id')::uuid = g.glossary_term_id)`, domainID, snapshot); err != nil {
		return nil, fmt.Errorf("restoring term domains: %w", err)
	}
	// The name stays: it is the domain's, and other things hang from it.
	if _, err := tx.Exec(ctx, `
		UPDATE domains SET description = $2::jsonb->'domain'->>'description',
			metadata = jsonb_set(metadata, '{ontology}', COALESCE($2::jsonb->'domain'->'ontology', '{}'::jsonb)), updated_at = now()
		WHERE id = $1`, domainID, snapshot); err != nil {
		return nil, fmt.Errorf("restoring domain: %w", err)
	}
	return saved, tx.Commit(ctx)
}
