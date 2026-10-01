package quality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marmotdata/marmot/internal/core/metamodel"
)

type PostgresRuleRepository struct{ db *pgxpool.Pool }

func NewPostgresRuleRepository(db *pgxpool.Pool) *PostgresRuleRepository {
	return &PostgresRuleRepository{db: db}
}

const customColumns = `rule, enabled, version, COALESCE(created_by, ''), COALESCE(updated_by, ''), created_at, updated_at`

func scanCustom(row pgx.Row) (*CustomRule, error) {
	var (
		raw  []byte
		rule CustomRule
	)
	if err := row.Scan(&raw, &rule.Enabled, &rule.Version, &rule.CreatedBy, &rule.UpdatedBy, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
		return nil, err
	}
	var definition metamodel.QualityRule
	if err := json.Unmarshal(raw, &definition); err != nil {
		return nil, fmt.Errorf("decoding a custom quality rule: %w", err)
	}
	rule.QualityRule = definition
	return &rule, nil
}

func (r *PostgresRuleRepository) List(ctx context.Context) ([]CustomRule, error) {
	rows, err := r.db.Query(ctx, `SELECT `+customColumns+` FROM quality_custom_rules ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CustomRule{}
	for rows.Next() {
		rule, err := scanCustom(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rule)
	}
	return out, rows.Err()
}

func (r *PostgresRuleRepository) Get(ctx context.Context, id string) (*CustomRule, error) {
	rule, err := scanCustom(r.db.QueryRow(ctx, `SELECT `+customColumns+` FROM quality_custom_rules WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRuleNotFound
	}
	return rule, err
}

func (r *PostgresRuleRepository) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM quality_custom_rules`).Scan(&n)
	return n, err
}

func (r *PostgresRuleRepository) Create(ctx context.Context, rule CustomRule) (*CustomRule, error) {
	raw, err := json.Marshal(rule.QualityRule)
	if err != nil {
		return nil, err
	}
	created, err := scanCustom(r.db.QueryRow(ctx, `
		INSERT INTO quality_custom_rules (id, rule, enabled, created_by, updated_by)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''))
		RETURNING `+customColumns, rule.ID, raw, rule.Enabled, rule.CreatedBy, rule.UpdatedBy))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrRuleExists
	}
	return created, err
}

func (r *PostgresRuleRepository) Update(ctx context.Context, rule CustomRule, expected int64) (*CustomRule, error) {
	raw, err := json.Marshal(rule.QualityRule)
	if err != nil {
		return nil, err
	}
	updated, err := scanCustom(r.db.QueryRow(ctx, `
		UPDATE quality_custom_rules
		   SET rule = $2, enabled = $3, version = version + 1, updated_by = NULLIF($4, ''), updated_at = now()
		 WHERE id = $1 AND version = $5
		RETURNING `+customColumns, rule.ID, raw, rule.Enabled, rule.UpdatedBy, expected))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := r.Get(ctx, rule.ID); errors.Is(getErr, ErrRuleNotFound) {
			return nil, ErrRuleNotFound
		}
		return nil, ErrRuleVersion
	}
	return updated, err
}

func (r *PostgresRuleRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM quality_custom_rules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRuleNotFound
	}
	return nil
}
