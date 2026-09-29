package quality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrVersionConflict = errors.New("quality settings version conflict")
	ErrVersionRequired = errors.New("quality settings version required")
)

// Repository stores the settings row. Save is compare-and-set: expected is the
// version the caller read, and 0 means "nobody has saved yet".
type Repository interface {
	Get(ctx context.Context) (*Stored, error)
	Save(ctx context.Context, settings Settings, expected int64, by string) (*Stored, error)
}

type PostgresRepository struct{ db *pgxpool.Pool }

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

const selectSettings = `SELECT settings, version, updated_at, COALESCE(updated_by, '') FROM quality_settings WHERE id = 1`

func scan(row pgx.Row) (*Stored, error) {
	var (
		raw []byte
		out Stored
	)
	if err := row.Scan(&raw, &out.Version, &out.UpdatedAt, &out.UpdatedBy); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &out.Settings); err != nil {
		return nil, fmt.Errorf("decoding quality settings: %w", err)
	}
	return &out, nil
}

// Get returns the saved settings, or nil when nobody has saved any.
func (r *PostgresRepository) Get(ctx context.Context) (*Stored, error) {
	stored, err := scan(r.db.QueryRow(ctx, selectSettings))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return stored, err
}

func (r *PostgresRepository) Save(ctx context.Context, settings Settings, expected int64, by string) (*Stored, error) {
	raw, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("encoding quality settings: %w", err)
	}
	var row pgx.Row
	if expected == 0 {
		row = r.db.QueryRow(ctx, `
			INSERT INTO quality_settings (id, settings, updated_by) VALUES (1, $1, NULLIF($2, ''))
			ON CONFLICT (id) DO NOTHING
			RETURNING settings, version, updated_at, COALESCE(updated_by, '')`, raw, by)
	} else {
		row = r.db.QueryRow(ctx, `
			UPDATE quality_settings
			   SET settings = $1, version = version + 1, updated_at = $4, updated_by = NULLIF($2, '')
			 WHERE id = 1 AND version = $3
			RETURNING settings, version, updated_at, COALESCE(updated_by, '')`, raw, by, expected, time.Now())
	}
	stored, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVersionConflict
	}
	return stored, err
}
