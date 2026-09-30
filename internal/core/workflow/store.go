package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrConflict is a concurrent change: a lost race on a definition version
	// or on an instance revision.
	ErrConflict = errors.New("conflict")
)

// Definition statuses.
const (
	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusRetired   = "retired"
)

// Instance statuses.
const (
	InstanceRunning   = "running"
	InstanceCompleted = "completed"
	InstanceFailed    = "failed"
	InstanceCancelled = "cancelled"
)

// Task statuses.
const (
	TaskOpen      = "open"
	TaskCompleted = "completed"
	TaskCancelled = "cancelled"
)

type Definition struct {
	ID          string     `json:"id"`
	ProcessKey  string     `json:"process_key"`
	Name        string     `json:"name"`
	Version     int        `json:"version"`
	Status      string     `json:"status"`
	BPMN        string     `json:"bpmn,omitempty"`
	CreatedBy   *string    `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	// Issues is filled on read: what keeps a draft from being published.
	Issues []Issue `json:"issues"`
	// Schedule is filled on read when the definition starts itself on a timer.
	Schedule *ScheduleInfo `json:"schedule,omitempty"`
}

// ScheduleInfo is what a client shows of a timer start.
type ScheduleInfo struct {
	Cycle     string     `json:"cycle"`
	Query     string     `json:"query,omitempty"`
	NextRunAt time.Time  `json:"next_run_at"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	Enabled   bool       `json:"enabled"`
}

// Schedule is a timer start that is due.
type Schedule struct {
	DefinitionID string
	Cycle        string
	Query        string
	RunAs        string
}

type Instance struct {
	ID             string     `json:"id"`
	DefinitionID   string     `json:"definition_id"`
	DefinitionName string     `json:"definition_name"`
	Version        int        `json:"version"`
	Status         string     `json:"status"`
	TargetKind     *string    `json:"target_kind,omitempty"`
	TargetID       *string    `json:"target_id,omitempty"`
	TargetName     *string    `json:"target_name,omitempty"`
	State          *State     `json:"-"`
	Revision       int        `json:"-"`
	InitiatorID    *string    `json:"initiator_id,omitempty"`
	FailureCode    *string    `json:"failure_code,omitempty"`
	FailureElement *string    `json:"failure_element,omitempty"`
	FailureDetail  *string    `json:"failure_detail,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
}

type Task struct {
	ID          string     `json:"id"`
	InstanceID  string     `json:"instance_id"`
	TokenID     string     `json:"-"`
	NodeID      string     `json:"node_id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Candidates  []string   `json:"candidates"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	TimerNode   *string    `json:"-"`
	Decision    *string    `json:"decision,omitempty"`
	Comment     *string    `json:"comment,omitempty"`
	CompletedBy *string    `json:"completed_by,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	// Filled when listed as an inbox, from the instance.
	DefinitionName string  `json:"definition_name,omitempty"`
	TargetKind     *string `json:"target_kind,omitempty"`
	TargetID       *string `json:"target_id,omitempty"`
	TargetName     *string `json:"target_name,omitempty"`
	// FormFields are governed field ids the task expects when completing.
	FormFields []string `json:"form_fields,omitempty"`
	// Wait marks a timed wait, which nobody decides: it ends when DueAt passes.
	Wait bool `json:"wait,omitempty"`
	// RemindAt is when the candidates are reminded that the task is due.
	RemindAt *time.Time `json:"-"`
}

type Event struct {
	ID        int64          `json:"id"`
	Type      string         `json:"type"`
	Element   *string        `json:"element,omitempty"`
	ActorID   *string        `json:"actor_id,omitempty"`
	Detail    map[string]any `json:"detail,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PostgresRepository stores workflows. Its methods run on the pool, or on the
// transaction of WithTx.
type PostgresRepository struct {
	pool *pgxpool.Pool
	db   querier
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool, db: pool}
}

// WithTx runs fn in one transaction.
func (r *PostgresRepository) WithTx(ctx context.Context, fn func(tx *PostgresRepository) error) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		return fn(&PostgresRepository{pool: r.pool, db: tx})
	})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

const definitionColumns = `id, process_key, name, version, status, bpmn, created_by::text, created_at, updated_at, published_at`

func scanDefinition(row pgx.Row) (*Definition, error) {
	d := &Definition{}
	err := row.Scan(&d.ID, &d.ProcessKey, &d.Name, &d.Version, &d.Status, &d.BPMN, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt, &d.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// CreateDefinition stores a draft as the next version of its process key.
func (r *PostgresRepository) CreateDefinition(ctx context.Context, key, name, bpmn string, createdBy *string) (*Definition, error) {
	d, err := scanDefinition(r.db.QueryRow(ctx, `
		INSERT INTO workflow_definitions (process_key, name, version, bpmn, created_by)
		SELECT $1, $2, COALESCE(MAX(version), 0) + 1, $3, $4
		  FROM workflow_definitions WHERE process_key = $1
		RETURNING `+definitionColumns, key, name, bpmn, createdBy))
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	return d, err
}

// UpsertSchedule stores the timer start of a definition and switches off the schedules of
// its other versions, so a new version replaces the old one instead of doubling the runs.
func (r *PostgresRepository) UpsertSchedule(ctx context.Context, definitionID, processKey, cycle, query, runAs string, next time.Time) error {
	if _, err := r.db.Exec(ctx, `
		UPDATE workflow_schedules SET enabled = false
		 WHERE definition_id::text <> $1
		   AND definition_id IN (SELECT id FROM workflow_definitions WHERE process_key = $2)`, definitionID, processKey); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO workflow_schedules (definition_id, cycle, query, run_as, next_run_at)
		VALUES ($1::uuid, $2, $3, NULLIF($4, '')::uuid, $5)
		ON CONFLICT (definition_id) DO UPDATE
		   SET cycle = $2, query = $3, run_as = NULLIF($4, '')::uuid, next_run_at = $5, enabled = true, last_error = NULL`,
		definitionID, cycle, query, runAs, next)
	return err
}

func (r *PostgresRepository) DisableSchedule(ctx context.Context, definitionID string) error {
	_, err := r.db.Exec(ctx, `UPDATE workflow_schedules SET enabled = false WHERE definition_id::text = $1`, definitionID)
	return err
}

// ClaimSchedules takes the schedules that are due and moves each to its next fire before
// returning it, so a start that fails does not come back on the next tick and two replicas
// never take the same one.
func (r *PostgresRepository) ClaimSchedules(ctx context.Context, now time.Time, limit int) ([]*Schedule, error) {
	rows, err := r.db.Query(ctx, `
		SELECT definition_id::text, cycle, query, COALESCE(run_as::text, '')
		  FROM workflow_schedules
		 WHERE enabled AND next_run_at <= $1
		 ORDER BY next_run_at
		 LIMIT $2
		 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	var out []*Schedule
	for rows.Next() {
		sc := &Schedule{}
		if err := rows.Scan(&sc.DefinitionID, &sc.Cycle, &sc.Query, &sc.RunAs); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, sc)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, sc := range out {
		next := now.Add(24 * time.Hour)
		if cycle, err := ParseCycle(sc.Cycle); err == nil {
			next = cycle.Next(now)
		}
		if _, err := r.db.Exec(ctx, `UPDATE workflow_schedules SET next_run_at = $2, last_run_at = $3 WHERE definition_id::text = $1`, sc.DefinitionID, next, now); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *PostgresRepository) SetScheduleResult(ctx context.Context, definitionID, failure string) error {
	_, err := r.db.Exec(ctx, `UPDATE workflow_schedules SET last_error = NULLIF($2, '') WHERE definition_id::text = $1`, definitionID, failure)
	return err
}

// SchedulesOf returns the schedule of each definition that has one, by definition id.
func (r *PostgresRepository) SchedulesOf(ctx context.Context, ids []string) (map[string]*ScheduleInfo, error) {
	out := map[string]*ScheduleInfo{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT definition_id::text, cycle, query, next_run_at, last_run_at, COALESCE(last_error, ''), enabled
		  FROM workflow_schedules WHERE definition_id::text = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		info := &ScheduleInfo{}
		if err := rows.Scan(&id, &info.Cycle, &info.Query, &info.NextRunAt, &info.LastRunAt, &info.LastError, &info.Enabled); err != nil {
			return nil, err
		}
		out[id] = info
	}
	return out, rows.Err()
}

func (r *PostgresRepository) GetDefinition(ctx context.Context, id string) (*Definition, error) {
	return scanDefinition(r.db.QueryRow(ctx, `SELECT `+definitionColumns+` FROM workflow_definitions WHERE id::text = $1`, id))
}

// ListDefinitions returns every version, newest first within a key, without
// the BPMN body.
func (r *PostgresRepository) ListDefinitions(ctx context.Context, status string) ([]*Definition, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+definitionColumns+` FROM workflow_definitions
		 WHERE $1 = '' OR status = $1
		 ORDER BY lower(name), process_key, version DESC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Definition{}
	for rows.Next() {
		d, err := scanDefinition(rows)
		if err != nil {
			return nil, err
		}
		d.BPMN = ""
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDraft replaces the document of a draft. Published and retired
// versions are frozen: instances run on them.
func (r *PostgresRepository) UpdateDraft(ctx context.Context, id, name, bpmn string) (*Definition, error) {
	d, err := scanDefinition(r.db.QueryRow(ctx, `
		UPDATE workflow_definitions SET name = $2, bpmn = $3, updated_at = now()
		 WHERE id::text = $1 AND status = 'draft'
		RETURNING `+definitionColumns, id, name, bpmn))
	if errors.Is(err, ErrNotFound) {
		if _, getErr := r.GetDefinition(ctx, id); getErr == nil {
			return nil, ErrConflict
		}
	}
	return d, err
}

// SetDefinitionStatus moves a definition from one status to another.
func (r *PostgresRepository) SetDefinitionStatus(ctx context.Context, id, from, to string) (*Definition, error) {
	d, err := scanDefinition(r.db.QueryRow(ctx, `
		UPDATE workflow_definitions
		   SET status = $3, updated_at = now(),
		       published_at = CASE WHEN $3 = 'published' THEN now() ELSE published_at END
		 WHERE id::text = $1 AND status = $2
		RETURNING `+definitionColumns, id, from, to))
	if errors.Is(err, ErrNotFound) {
		if _, getErr := r.GetDefinition(ctx, id); getErr == nil {
			return nil, ErrConflict
		}
	}
	return d, err
}

// DeleteDraft removes a draft nobody ran.
func (r *PostgresRepository) DeleteDraft(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM workflow_definitions WHERE id::text = $1 AND status = 'draft'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, getErr := r.GetDefinition(ctx, id); getErr == nil {
			return ErrConflict
		}
		return ErrNotFound
	}
	return nil
}

const instanceColumns = `i.id, i.definition_id, d.name, d.version, i.status, i.target_kind, i.target_id, i.target_name,
	i.state, i.revision, i.initiator_id::text, i.failure_code, i.failure_element, i.failure_detail, i.created_at, i.updated_at, i.ended_at`

func scanInstance(row pgx.Row) (*Instance, error) {
	in := &Instance{}
	var state []byte
	err := row.Scan(&in.ID, &in.DefinitionID, &in.DefinitionName, &in.Version, &in.Status, &in.TargetKind, &in.TargetID, &in.TargetName,
		&state, &in.Revision, &in.InitiatorID, &in.FailureCode, &in.FailureElement, &in.FailureDetail, &in.CreatedAt, &in.UpdatedAt, &in.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	in.State = &State{}
	if err := json.Unmarshal(state, in.State); err != nil {
		return nil, fmt.Errorf("decoding instance state: %w", err)
	}
	return in, nil
}

func (r *PostgresRepository) InsertInstance(ctx context.Context, in *Instance) error {
	state, err := json.Marshal(in.State)
	if err != nil {
		return err
	}
	return r.db.QueryRow(ctx, `
		INSERT INTO workflow_instances (definition_id, status, target_kind, target_id, target_name, state, initiator_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, revision, created_at, updated_at`,
		in.DefinitionID, in.Status, in.TargetKind, in.TargetID, in.TargetName, state, in.InitiatorID,
	).Scan(&in.ID, &in.Revision, &in.CreatedAt, &in.UpdatedAt)
}

// GetInstance reads an instance; lock takes a row lock for the transaction.
func (r *PostgresRepository) GetInstance(ctx context.Context, id string, lock bool) (*Instance, error) {
	sql := `SELECT ` + instanceColumns + ` FROM workflow_instances i JOIN workflow_definitions d ON d.id = i.definition_id WHERE i.id::text = $1`
	if lock {
		sql += ` FOR UPDATE OF i`
	}
	return scanInstance(r.db.QueryRow(ctx, sql, id))
}

// SaveInstance writes the state and status, if nobody changed the instance
// since it was read.
func (r *PostgresRepository) SaveInstance(ctx context.Context, in *Instance) error {
	state, err := json.Marshal(in.State)
	if err != nil {
		return err
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE workflow_instances
		   SET state = $3, status = $4, failure_code = $5, failure_element = $6, failure_detail = $7,
		       revision = revision + 1, updated_at = now(),
		       ended_at = CASE WHEN $4 <> 'running' THEN now() ELSE NULL END
		 WHERE id::text = $1 AND revision = $2`,
		in.ID, in.Revision, state, in.Status, in.FailureCode, in.FailureElement, in.FailureDetail)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	in.Revision++
	return nil
}

// InstanceFilter narrows ListInstances. Participant limits to instances the
// user started or has a task in; empty means every instance.
type InstanceFilter struct {
	Participant string
	Status      string
	TargetKind  string
	TargetID    string
	Limit       int
}

func (r *PostgresRepository) ListInstances(ctx context.Context, f InstanceFilter) ([]*Instance, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+instanceColumns+`
		  FROM workflow_instances i JOIN workflow_definitions d ON d.id = i.definition_id
		 WHERE ($1 = '' OR i.initiator_id::text = $1 OR EXISTS (
		           SELECT 1 FROM workflow_tasks t
		            WHERE t.instance_id = i.id AND ($1 = ANY(t.candidates::text[]) OR t.completed_by::text = $1)))
		   AND ($2 = '' OR i.status = $2)
		   AND ($3 = '' OR i.target_kind = $3)
		   AND ($4 = '' OR i.target_id = $4)
		 ORDER BY i.created_at DESC
		 LIMIT $5`, f.Participant, f.Status, f.TargetKind, f.TargetID, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Instance{}
	for rows.Next() {
		in, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

const taskColumns = `t.id, t.instance_id, t.token_id, t.node_id, t.name, t.status, t.candidates::text[], t.due_at, t.timer_node,
	t.decision, t.comment, t.completed_by::text, t.completed_at, t.created_at, t.form_fields, t.remind_at`

func scanTask(row pgx.Row, extra ...any) (*Task, error) {
	t := &Task{}
	dest := []any{&t.ID, &t.InstanceID, &t.TokenID, &t.NodeID, &t.Name, &t.Status, &t.Candidates, &t.DueAt, &t.TimerNode,
		&t.Decision, &t.Comment, &t.CompletedBy, &t.CompletedAt, &t.CreatedAt, &t.FormFields, &t.RemindAt}
	if len(extra) > 0 {
		dest = append(dest, &t.DefinitionName, &t.TargetKind, &t.TargetID, &t.TargetName)
	}
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if t.Candidates == nil {
		t.Candidates = []string{}
	}
	if len(t.FormFields) == 0 {
		t.FormFields = nil
	}
	return t, err
}

func (r *PostgresRepository) InsertTask(ctx context.Context, t *Task) error {
	return r.db.QueryRow(ctx, `
		INSERT INTO workflow_tasks (instance_id, token_id, node_id, name, candidates, due_at, timer_node, form_fields, remind_at)
		VALUES ($1, $2, $3, $4, $5::uuid[], $6, $7, $8::text[], $9)
		RETURNING id, status, created_at`,
		t.InstanceID, t.TokenID, t.NodeID, t.Name, t.Candidates, t.DueAt, t.TimerNode, nonNil(t.FormFields), t.RemindAt,
	).Scan(&t.ID, &t.Status, &t.CreatedAt)
}

func (r *PostgresRepository) GetTask(ctx context.Context, id string) (*Task, error) {
	return scanTask(r.db.QueryRow(ctx, `SELECT `+taskColumns+` FROM workflow_tasks t WHERE t.id::text = $1`, id))
}

func (r *PostgresRepository) ListTasks(ctx context.Context, instanceID string) ([]*Task, error) {
	rows, err := r.db.Query(ctx, `SELECT `+taskColumns+` FROM workflow_tasks t WHERE t.instance_id::text = $1 ORDER BY t.created_at, t.id`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// OpenTasksFor lists the open tasks a user is a candidate for.
func (r *PostgresRepository) OpenTasksFor(ctx context.Context, userID string) ([]*Task, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+taskColumns+`, d.name, i.target_kind, i.target_id, i.target_name
		  FROM workflow_tasks t
		  JOIN workflow_instances i ON i.id = t.instance_id
		  JOIN workflow_definitions d ON d.id = i.definition_id
		 WHERE t.status = 'open' AND t.candidates @> ARRAY[$1::uuid]
		 ORDER BY t.due_at NULLS LAST, t.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CloseTask marks an open task completed or cancelled.
func (r *PostgresRepository) CloseTask(ctx context.Context, instanceID, tokenID, status string, decision, comment, by *string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE workflow_tasks
		   SET status = $3, decision = $4, comment = $5, completed_by = $6::uuid, completed_at = now()
		 WHERE instance_id::text = $1 AND token_id = $2 AND status = 'open'`,
		instanceID, tokenID, status, decision, comment, by)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// CancelOpenTasks closes every open task of an instance.
func (r *PostgresRepository) CancelOpenTasks(ctx context.Context, instanceID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE workflow_tasks SET status = 'cancelled', completed_at = now()
		 WHERE instance_id::text = $1 AND status = 'open'`, instanceID)
	return err
}

// DueTimers returns open tasks whose timer is due and has not fired.
func (r *PostgresRepository) DueTimers(ctx context.Context, now time.Time, limit int) ([]*Task, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+taskColumns+` FROM workflow_tasks t
		 WHERE t.status = 'open' AND NOT t.timer_fired AND t.timer_node IS NOT NULL AND t.due_at <= $1
		 ORDER BY t.due_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// DueReminders lists open tasks whose reminder time has come and that have not been reminded.
func (r *PostgresRepository) DueReminders(ctx context.Context, now time.Time, limit int) ([]*Task, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+taskColumns+`
		  FROM workflow_tasks t
		 WHERE t.status = 'open' AND t.reminded = false AND t.remind_at IS NOT NULL AND t.remind_at <= $1
		 ORDER BY t.remind_at
		 LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *PostgresRepository) MarkReminded(ctx context.Context, taskID string) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE workflow_tasks SET reminded = true WHERE id::text = $1 AND reminded = false`, taskID)
	return tag.RowsAffected() > 0, err
}

func (r *PostgresRepository) MarkTimerFired(ctx context.Context, taskID string) error {
	_, err := r.db.Exec(ctx, `UPDATE workflow_tasks SET timer_fired = true WHERE id::text = $1`, taskID)
	return err
}

func (r *PostgresRepository) InsertEvents(ctx context.Context, instanceID string, actorID *string, events []StepEvent) error {
	for _, e := range events {
		var detail []byte
		if len(e.Detail) > 0 {
			var err error
			if detail, err = json.Marshal(e.Detail); err != nil {
				return err
			}
		}
		var element *string
		if e.Element != "" {
			element = &e.Element
		}
		if _, err := r.db.Exec(ctx, `
			INSERT INTO workflow_events (instance_id, type, element, actor_id, detail)
			VALUES ($1, $2, $3, $4::uuid, $5)`, instanceID, e.Type, element, actorID, detail); err != nil {
			return err
		}
	}
	return nil
}

func (r *PostgresRepository) ListEvents(ctx context.Context, instanceID string) ([]*Event, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, type, element, actor_id::text, detail, created_at
		  FROM workflow_events WHERE instance_id::text = $1 ORDER BY id`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Event{}
	for rows.Next() {
		e := &Event{}
		var detail []byte
		if err := rows.Scan(&e.ID, &e.Type, &e.Element, &e.ActorID, &detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(detail) > 0 {
			if err := json.Unmarshal(detail, &e.Detail); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
