package quality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const runColumns = `id, trigger, COALESCE(triggered_by, ''), status, settings_version, metamodel_profile,
	metamodel_version, metamodel_hash, processed, total, scores_written, score_conflicts, score_failures,
	started_at, finished_at, COALESCE(error, ''), summary`

func scanRun(row pgx.Row, withSettings bool) (*Run, error) {
	var (
		run     Run
		summary []byte
		raw     []byte
	)
	dest := []any{&run.ID, &run.Trigger, &run.TriggeredBy, &run.Status, &run.SettingsVersion, &run.MetamodelProfile,
		&run.MetamodelVersion, &run.MetamodelHash, &run.Processed, &run.Total,
		&run.ScoresWritten, &run.ScoreConflicts, &run.ScoreFailures, &run.StartedAt, &run.FinishedAt, &run.Error, &summary}
	if withSettings {
		dest = append(dest, &raw)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	if len(summary) > 0 {
		run.Summary = new(Summary)
		if err := json.Unmarshal(summary, run.Summary); err != nil {
			return nil, fmt.Errorf("decoding quality run summary: %w", err)
		}
	}
	if withSettings && len(raw) > 0 {
		run.Settings = new(Settings)
		if err := json.Unmarshal(raw, run.Settings); err != nil {
			return nil, fmt.Errorf("decoding quality run settings: %w", err)
		}
	}
	return &run, nil
}

func (r *PostgresRepository) Reap(ctx context.Context, silentFor time.Duration) error {
	_, err := r.db.Exec(ctx, `
		UPDATE quality_runs SET status = 'failed', finished_at = now(), error = 'interrupted'
		 WHERE status = 'running' AND heartbeat_at < $1`, time.Now().Add(-silentFor))
	return err
}

func (r *PostgresRepository) LastStarted(ctx context.Context) (time.Time, error) {
	var started *time.Time
	if err := r.db.QueryRow(ctx, `SELECT max(started_at) FROM quality_runs`).Scan(&started); err != nil {
		return time.Time{}, err
	}
	if started == nil {
		return time.Time{}, nil
	}
	return *started, nil
}

func (r *PostgresRepository) Start(ctx context.Context, run Run, settings Settings) (*Run, error) {
	raw, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("encoding quality run settings: %w", err)
	}
	row := r.db.QueryRow(ctx, `
		INSERT INTO quality_runs (trigger, triggered_by, settings_version, settings, metamodel_profile, metamodel_version, metamodel_hash)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7)
		RETURNING `+runColumns, run.Trigger, run.TriggeredBy, run.SettingsVersion, raw, run.MetamodelProfile, run.MetamodelVersion, run.MetamodelHash)
	started, err := scanRun(row, false)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		current, getErr := scanRun(r.db.QueryRow(ctx, `SELECT `+runColumns+` FROM quality_runs WHERE status = 'running'`), false)
		if getErr != nil {
			return nil, ErrRunInProgress
		}
		return current, ErrRunInProgress
	}
	return started, err
}

func (r *PostgresRepository) SetTotal(ctx context.Context, id string, total int) error {
	_, err := r.db.Exec(ctx, `UPDATE quality_runs SET total = $2, heartbeat_at = now() WHERE id = $1`, id, total)
	return err
}

func (r *PostgresRepository) SaveBatch(ctx context.Context, id string, results []AssetResult, lastAssetID string, processed int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows := make([][]any, len(results))
	var issues [][]any
	for i, res := range results {
		rows[i] = []any{id, res.AssetID, res.MRN, res.Name, res.Type, res.DomainID,
			res.Completeness, res.Conformity, res.Quality, string(res.Status), res.IssueCount}
		for _, issue := range res.Issues {
			issues = append(issues, []any{id, res.AssetID, issue.FieldID, issue.Code, string(issue.RuleID), string(issue.Severity), issue.Section, issue.Item})
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"quality_results"},
		[]string{"run_id", "asset_id", "asset_mrn", "asset_name", "asset_type", "domain_id", "completeness", "conformity", "quality", "status", "issue_count"},
		pgx.CopyFromRows(rows)); err != nil {
		return fmt.Errorf("storing quality results: %w", err)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"quality_issues"},
		[]string{"run_id", "asset_id", "field_id", "code", "rule_id", "severity", "section", "item"},
		pgx.CopyFromRows(issues)); err != nil {
		return fmt.Errorf("storing quality issues: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE quality_runs SET processed = $2, last_asset_id = $3, heartbeat_at = now() WHERE id = $1`, id, processed, lastAssetID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) AddScoreCounts(ctx context.Context, id string, counts ScoreCounts) error {
	_, err := r.db.Exec(ctx, `
		UPDATE quality_runs
		   SET scores_written = scores_written + $2, score_conflicts = score_conflicts + $3,
		       score_failures = score_failures + $4, heartbeat_at = now()
		 WHERE id = $1`, id, counts.Written, counts.Conflicts, counts.Failed)
	return err
}

func (r *PostgresRepository) Finish(ctx context.Context, id string, summary Summary, retention Retention) error {
	raw, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("encoding quality summary: %w", err)
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `UPDATE quality_runs SET status = 'succeeded', finished_at = now(), summary = $2, heartbeat_at = now() WHERE id = $1`, id, raw); err != nil {
		return err
	}
	// Only the run that just finished keeps the detail of its findings.
	if _, err := tx.Exec(ctx, `DELETE FROM quality_issues WHERE run_id <> $1`, id); err != nil {
		return err
	}
	// A retention of 0 keeps the history of nothing but the latest run, never an empty page.
	if _, err := tx.Exec(ctx, `
		DELETE FROM quality_results r USING quality_runs q
		 WHERE r.run_id = q.id AND r.run_id <> $1 AND q.started_at < now() - make_interval(days => $2)`, id, retention.ResultDays); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM quality_runs WHERE id <> $1 AND status <> 'running' AND started_at < now() - make_interval(days => $2)`, id, retention.RunDays); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRepository) Fail(ctx context.Context, id string, reason string) error {
	_, err := r.db.Exec(ctx, `UPDATE quality_runs SET status = 'failed', finished_at = now(), error = $2 WHERE id = $1 AND status = 'running'`, id, reason)
	return err
}

func (r *PostgresRepository) Runs(ctx context.Context, limit, offset int) ([]Run, int, error) {
	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM quality_runs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.Query(ctx, `SELECT `+runColumns+` FROM quality_runs ORDER BY started_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		run, err := scanRun(rows, false)
		if err != nil {
			return nil, 0, err
		}
		runs = append(runs, *run)
	}
	return runs, total, rows.Err()
}

func (r *PostgresRepository) Run(ctx context.Context, id string) (*Run, error) {
	run, err := scanRun(r.db.QueryRow(ctx, `SELECT `+runColumns+`, settings FROM quality_runs WHERE id::text = $1`, id), true)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRunNotFound
	}
	return run, err
}

func (r *PostgresRepository) Results(ctx context.Context, id string, f ResultFilter) ([]AssetResult, int, error) {
	where, args := []string{"run_id::text = $1"}, []any{id}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.Status != "" {
		add("status = $%d", string(f.Status))
	}
	if f.Domain != "" {
		add("domain_id = $%d", f.Domain)
	}
	if f.Type != "" {
		add("lower(asset_type) = lower($%d)", f.Type)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		add("(position(lower($%[1]d) in lower(asset_name)) > 0 OR position(lower($%[1]d) in lower(asset_mrn)) > 0)", q)
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM quality_results WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "quality ASC, asset_name ASC, asset_id ASC"
	if f.Sort == "name" {
		order = "asset_name ASC, asset_id ASC"
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT asset_id, asset_mrn, asset_name, asset_type, domain_id, completeness, conformity, quality, status, issue_count
		  FROM quality_results WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d`, clause, order, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	results := []AssetResult{}
	index := map[string]int{}
	for rows.Next() {
		var res AssetResult
		if err := rows.Scan(&res.AssetID, &res.MRN, &res.Name, &res.Type, &res.DomainID, &res.Completeness, &res.Conformity, &res.Quality, &res.Status, &res.IssueCount); err != nil {
			return nil, 0, err
		}
		index[res.AssetID] = len(results)
		results = append(results, res)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(results) == 0 {
		return results, total, nil
	}

	ids := make([]string, 0, len(index))
	for assetID := range index {
		ids = append(ids, assetID)
	}
	detail, err := r.db.Query(ctx, `
		SELECT asset_id, field_id, code, rule_id, severity, section, item
		  FROM quality_issues WHERE run_id::text = $1 AND asset_id = ANY($2) ORDER BY asset_id, field_id, code, item`, id, ids)
	if err != nil {
		return nil, 0, err
	}
	defer detail.Close()
	for detail.Next() {
		var (
			assetID string
			issue   Issue
		)
		if err := detail.Scan(&assetID, &issue.FieldID, &issue.Code, &issue.RuleID, &issue.Severity, &issue.Section, &issue.Item); err != nil {
			return nil, 0, err
		}
		results[index[assetID]].Issues = append(results[index[assetID]].Issues, issue)
	}
	return results, total, detail.Err()
}

// DocumentedAssets says which of the assets, by MRN, have a documentation page of their own or the
// older per-source documentation.
func (r *PostgresRepository) DocumentedAssets(ctx context.Context, mrns []string) (map[string]bool, error) {
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
func (r *PostgresRepository) AssetDomains(ctx context.Context, assetIDs []string) (map[string]string, error) {
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
