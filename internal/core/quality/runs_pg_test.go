package quality_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/quality"
	"github.com/marmotdata/marmot/internal/metrics"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

type dbRecorder struct{ metrics.Recorder }

func (dbRecorder) RecordDBQuery(context.Context, string, time.Duration, bool) {}

const financeDomain = "11111111-1111-4111-8111-111111111111"

type env struct {
	pool *pgxpool.Pool
	repo *quality.PostgresRepository
	svc  quality.RunService
}

func pgEnv(t *testing.T, settings quality.Settings) *env {
	t.Helper()
	return pgEnvWith(t, settings, registry(t), false)
}

func pgEnvWith(t *testing.T, settings quality.Settings, reg *metamodel.Registry, write bool) *env {
	t.Helper()
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	assets := asset.NewPostgresRepository(pool, dbRecorder{})

	var admin string
	if err := pool.QueryRow(ctx, "SELECT id FROM users WHERE username = 'admin'").Scan(&admin); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		a := newAsset(fmt.Sprintf("pg%d", i), complete())
		a.Providers, a.CreatedBy = []string{"test"}, admin
		a.Schema, a.Sources, a.Environments = map[string]string{}, []asset.AssetSource{}, map[string]asset.Environment{}
		a.CreatedAt, a.UpdatedAt, a.LastSyncAt, a.Version = time.Now(), time.Now(), time.Now(), 1
		a.IsStub = i == 4
		if i == 1 {
			delete(a.Metadata["dgu"].(map[string]any), "classification")
		}
		if err := assets.Create(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, path, depth, name) VALUES ($1::uuid, '/'||$1::text||'/', 1, 'Finance')`, financeDomain); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO asset_domains (asset_id, domain_id) VALUES ('id-pg0', $1), ('id-pg1', $1)`, financeDomain); err != nil {
		t.Fatal(err)
	}

	repo := quality.NewPostgresRepository(pool)
	var options []quality.RunOption
	if write {
		options = append(options, quality.WithScoreWriter(asset.NewService(assets, asset.WithMetamodel(reg))))
	}
	svc := quality.NewRunService(fixedSettings{settings}, repo, assets, reg, options...)
	t.Cleanup(svc.Shutdown)
	return &env{pool: pool, repo: repo, svc: svc}
}

func (e *env) runToEnd(t *testing.T) *quality.Run {
	t.Helper()
	ctx := context.Background()
	run, err := e.svc.StartRun(ctx, quality.TriggerManual, "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, err := e.svc.Run(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != quality.RunRunning {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the run did not finish")
	return nil
}

func TestARunIsStoredAndReadBackWithItsResults(t *testing.T) {
	settings := quality.DefaultSettings()
	settings.BatchSize = 2
	e := pgEnv(t, settings)
	ctx := context.Background()

	run := e.runToEnd(t)
	if run.Status != quality.RunSucceeded || run.Processed != 5 || run.Total != 5 || run.FinishedAt == nil {
		t.Fatalf("run = %+v", run)
	}
	if run.Settings == nil || run.Settings.BatchSize != 2 || run.SettingsVersion != 3 || run.MetamodelHash == "" {
		t.Fatalf("what the run used: %+v", run)
	}
	if run.Summary == nil || run.Summary.TotalAssets != 4 || run.Summary.Stubs != 1 {
		t.Fatalf("summary = %+v", run.Summary)
	}
	byDomain := map[string]int{}
	for _, g := range run.Summary.ByDomain {
		byDomain[g.Key] = g.Count
	}
	if byDomain[financeDomain] != 2 || byDomain[quality.UnassignedDomain] != 2 {
		t.Fatalf("by domain = %v", byDomain)
	}

	results, total, err := e.svc.Results(ctx, run.ID, quality.ResultFilter{})
	if err != nil || total != 4 || len(results) != 4 {
		t.Fatalf("results = %d of %d, %v", len(results), total, err)
	}
	if results[0].AssetID != "id-pg1" || results[0].IssueCount != 1 || len(results[0].Issues) != 1 || results[0].Issues[0].Code != "required" {
		t.Fatalf("the weakest first, with its finding: %+v", results[0])
	}

	for name, f := range map[string]quality.ResultFilter{
		"domain":     {Domain: financeDomain},
		"unassigned": {Domain: quality.UnassignedDomain},
		"status":     {Status: quality.StatusWarning},
		"text":       {Query: "PG1"},
	} {
		got, n, err := e.svc.Results(ctx, run.ID, f)
		want := map[string]int{"domain": 2, "unassigned": 2, "status": 1, "text": 1}[name]
		if err != nil || n != want || len(got) != want {
			t.Errorf("%s: %d of %d, %v, want %d", name, len(got), n, err, want)
		}
	}
	page, n, _ := e.svc.Results(ctx, run.ID, quality.ResultFilter{Limit: 1, Offset: 3, Sort: "name"})
	if n != 4 || len(page) != 1 || page[0].AssetID != "id-pg3" {
		t.Fatalf("page = %+v of %d", page, n)
	}
	if _, _, err := e.svc.Results(ctx, "00000000-0000-4000-8000-0000000000ff", quality.ResultFilter{}); err != quality.ErrRunNotFound {
		t.Fatalf("an unknown run: %v", err)
	}
	if _, err := e.svc.Run(ctx, "not-a-uuid"); err != quality.ErrRunNotFound {
		t.Fatalf("a malformed id is not found, not a server error: %v", err)
	}
}

func TestNothingOfTheAuditBecomesACatalogAsset(t *testing.T) {
	e := pgEnv(t, quality.DefaultSettings())
	e.runToEnd(t)
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM assets`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("the catalog has %d assets after a run, %v", n, err)
	}
}

func TestOnlyOneRunRunsAtATimeAndAStaleOneIsReaped(t *testing.T) {
	e := pgEnv(t, quality.DefaultSettings())
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO quality_runs (trigger, settings_version, settings) VALUES ('manual', 1, '{}')`); err != nil {
		t.Fatal(err)
	}

	got, err := e.repo.Start(ctx, quality.Run{Trigger: quality.TriggerManual}, quality.DefaultSettings(), nil)
	if err != quality.ErrRunInProgress || got == nil || got.Status != quality.RunRunning {
		t.Fatalf("got %+v, %v", got, err)
	}

	if err := e.repo.Reap(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := e.repo.Start(ctx, quality.Run{Trigger: quality.TriggerManual}, quality.DefaultSettings(), nil); err != quality.ErrRunInProgress {
		t.Fatalf("a run that is alive was reaped: %v", err)
	}
	if err := e.repo.Reap(ctx, 0); err != nil {
		t.Fatal(err)
	}
	run, err := e.svc.StartRun(ctx, quality.TriggerManual, "")
	if err != nil {
		t.Fatalf("after reaping the dead run: %v", err)
	}
	if run.Status != quality.RunRunning {
		t.Fatalf("run = %+v", run)
	}
}

func TestOnlyTheLastRunKeepsItsFindingsAndRetentionPrunesTheRest(t *testing.T) {
	settings := quality.DefaultSettings()
	e := pgEnv(t, settings)
	ctx := context.Background()

	first := e.runToEnd(t)
	second := e.runToEnd(t)
	for run, want := range map[string]int{first.ID: 0, second.ID: 1} {
		var issues int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM quality_issues WHERE run_id::text = $1`, run).Scan(&issues); err != nil || issues != want {
			t.Fatalf("run %s keeps %d findings, want %d (%v)", run, issues, want, err)
		}
	}
	got, _, _ := e.svc.Results(ctx, first.ID, quality.ResultFilter{})
	if len(got) != 4 {
		t.Fatalf("the scores of the earlier run are history and stay: %d", len(got))
	}

	old := time.Now().Add(-100 * 24 * time.Hour)
	if _, err := e.pool.Exec(ctx, `UPDATE quality_runs SET started_at = $2 WHERE id::text = $1`, first.ID, old); err != nil {
		t.Fatal(err)
	}
	e.runToEnd(t)
	if _, n, _ := e.svc.Results(ctx, first.ID, quality.ResultFilter{}); n != 0 {
		t.Fatalf("results past 90 days were kept: %d", n)
	}
	if run, err := e.svc.Run(ctx, first.ID); err != nil || run.Summary == nil {
		t.Fatalf("the summary of a run outlives its results: %v", err)
	}

	if _, err := e.pool.Exec(ctx, `UPDATE quality_runs SET started_at = $1`, time.Now().Add(-800*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.runToEnd(t)
	runs, total, err := e.svc.Runs(ctx, 10, 0)
	if err != nil || total != 1 || len(runs) != 1 {
		t.Fatalf("runs past their retention were kept: %d, %v", total, err)
	}
}

func TestRetentionZeroKeepsOnlyTheLatestRun(t *testing.T) {
	settings := quality.DefaultSettings()
	settings.Retention = quality.Retention{}
	e := pgEnv(t, settings)
	ctx := context.Background()
	e.runToEnd(t)
	last := e.runToEnd(t)
	runs, total, err := e.svc.Runs(ctx, 10, 0)
	if err != nil || total != 1 || runs[0].ID != last.ID {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	if _, n, _ := e.svc.Results(ctx, last.ID, quality.ResultFilter{}); n != 4 {
		t.Fatalf("the latest run is never left empty: %d", n)
	}
}

func TestAFailedRunKeepsWhatEarlierBatchesStored(t *testing.T) {
	e := pgEnv(t, quality.DefaultSettings())
	ctx := context.Background()
	run, err := e.repo.Start(ctx, quality.Run{Trigger: quality.TriggerSchedule}, quality.DefaultSettings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.repo.SaveBatch(ctx, run.ID, []quality.AssetResult{{AssetID: "x", Status: quality.StatusWarning, DomainID: quality.UnassignedDomain}}, "x", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.Fail(ctx, run.ID, "disk full"); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.Run(ctx, run.ID)
	if err != nil || got.Status != quality.RunFailed || got.Error != "disk full" || got.Processed != 1 || got.FinishedAt == nil {
		t.Fatalf("run = %+v, %v", got, err)
	}
	if _, n, _ := e.svc.Results(ctx, run.ID, quality.ResultFilter{}); n != 1 {
		t.Fatalf("results of the batches that finished: %d", n)
	}
}

func TestTheLastRunStartedIsKnownToTheScheduler(t *testing.T) {
	e := pgEnv(t, quality.DefaultSettings())
	ctx := context.Background()
	if at, err := e.repo.LastStarted(ctx); err != nil || !at.IsZero() {
		t.Fatalf("before any run: %v %v", at, err)
	}
	run := e.runToEnd(t)
	at, err := e.repo.LastStarted(ctx)
	if err != nil || !at.Equal(run.StartedAt) {
		t.Fatalf("last started %v, run started %v (%v)", at, run.StartedAt, err)
	}
}

func storedAt(t *testing.T, e *env, id, key string) any {
	t.Helper()
	var value any
	if err := e.pool.QueryRow(context.Background(), `SELECT metadata->'dgu'->$2 FROM assets WHERE id = $1`, id, key).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func versionOf(t *testing.T, e *env, id string) int {
	t.Helper()
	var version int
	if err := e.pool.QueryRow(context.Background(), `SELECT version FROM assets WHERE id = $1`, id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func TestARunWritesTheScoresOnTheAssetsAndAnUnchangedAssetIsLeftAlone(t *testing.T) {
	e := pgEnvWith(t, quality.DefaultSettings(), scoreRegistry(t), true)
	// id-pg1 has no classification (a required field missing); make id-pg2 invalid as well, to
	// show that an asset with a bad value is still scored.
	if _, err := e.pool.Exec(context.Background(), `UPDATE assets SET metadata = jsonb_set(metadata, '{dgu,classification}', '"secret"') WHERE id = 'id-pg2'`); err != nil {
		t.Fatal(err)
	}

	first := e.runToEnd(t)
	if first.Status != quality.RunSucceeded || first.ScoresWritten != 4 || first.ScoreConflicts != 0 || first.ScoreFailures != 0 {
		t.Fatalf("run = %+v", first)
	}
	for _, id := range []string{"id-pg0", "id-pg1", "id-pg2", "id-pg3"} {
		if storedAt(t, e, id, "metadata_quality_score") == nil || storedAt(t, e, id, "metadata_quality_dimensions") == nil || storedAt(t, e, id, "metadata_quality_evaluated_at") == nil {
			t.Fatalf("%s was not scored", id)
		}
	}
	if storedAt(t, e, "id-pg0", "metadata_quality_score") != 1.0 {
		t.Fatalf("score of a complete asset: %v", storedAt(t, e, "id-pg0", "metadata_quality_score"))
	}
	if storedAt(t, e, "id-pg4", "metadata_quality_score") != nil {
		t.Fatal("a stub was scored")
	}

	versions := map[string]int{}
	for _, id := range []string{"id-pg0", "id-pg1", "id-pg2", "id-pg3"} {
		versions[id] = versionOf(t, e, id)
	}
	second := e.runToEnd(t)
	if second.ScoresWritten != 0 || second.ScoreFailures != 0 {
		t.Fatalf("an unchanged asset is not written again: %+v", second)
	}
	for id, version := range versions {
		if versionOf(t, e, id) != version {
			t.Errorf("%s changed version without changing its score", id)
		}
	}

	// Editing a field changes the score, and the next run says so.
	if _, err := e.pool.Exec(context.Background(), `UPDATE assets SET metadata = metadata #- '{dgu,data_steward}' WHERE id = 'id-pg0'`); err != nil {
		t.Fatal(err)
	}
	third := e.runToEnd(t)
	if third.ScoresWritten != 1 || storedAt(t, e, "id-pg0", "metadata_quality_score") == 1.0 {
		t.Fatalf("run = %+v, score %v", third, storedAt(t, e, "id-pg0", "metadata_quality_score"))
	}
}

func TestDocumentationPagesAndLegacyDocumentationCountAsDocumentationAvailable(t *testing.T) {
	e := pgEnv(t, quality.DefaultSettings())
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO doc_pages (entity_type, entity_id, title, content) VALUES ('asset', 'mrn://table/test/pg0', 'Notes', 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO documentation (mrn, content, source) VALUES ('mrn://table/test/pg1', 'old docs', 'plugin'), ('mrn://table/test/pg2', '   ', 'plugin')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO doc_pages (entity_type, entity_id, title, content) VALUES ('data_product', 'mrn://table/test/pg3', 'Not an asset page', 'x')`); err != nil {
		t.Fatal(err)
	}

	got, err := e.repo.DocumentedAssets(ctx, []string{"mrn://table/test/pg0", "mrn://table/test/pg1", "mrn://table/test/pg2", "mrn://table/test/pg3"})
	if err != nil || len(got) != 2 || !got["mrn://table/test/pg0"] || !got["mrn://table/test/pg1"] {
		t.Fatalf("documented = %v (%v)", got, err)
	}
	if none, err := e.repo.DocumentedAssets(ctx, nil); err != nil || len(none) != 0 {
		t.Fatalf("nothing asked: %v %v", none, err)
	}
}

func TestCustomRulesAreStoredWithCompareAndSet(t *testing.T) {
	pool := pgtest.TempDB(t)
	repo := quality.NewPostgresRuleRepository(pool)
	ctx := context.Background()
	rule := quality.CustomRule{
		Rule:    metamodel.QualityRule{ID: "rule_a", Name: "Steward", Dimension: "validity", Severity: "warning", Checks: []metamodel.QualityCondition{{Field: "data_steward", Op: metamodel.OpSet}}},
		Enabled: true, CreatedBy: "", UpdatedBy: "",
	}

	created, err := repo.Create(ctx, rule)
	if err != nil || created.Version != 1 || !created.Enabled || created.Checks[0].Op != metamodel.OpSet {
		t.Fatalf("%+v %v", created, err)
	}
	if _, err := repo.Create(ctx, rule); !errors.Is(err, quality.ErrRuleExists) {
		t.Fatalf("a taken id: %v", err)
	}

	rule.Name = "Steward is mandatory"
	rule.Enabled = false
	updated, err := repo.Update(ctx, rule, 1)
	if err != nil || updated.Version != 2 || updated.Enabled || updated.Name != "Steward is mandatory" {
		t.Fatalf("%+v %v", updated, err)
	}
	if _, err := repo.Update(ctx, rule, 1); !errors.Is(err, quality.ErrRuleVersion) {
		t.Fatalf("stale: %v", err)
	}
	rule.ID = "rule_gone"
	if _, err := repo.Update(ctx, rule, 1); !errors.Is(err, quality.ErrRuleNotFound) {
		t.Fatalf("unknown: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "rule_a" {
		t.Fatalf("%+v %v", list, err)
	}
	if n, _ := repo.Count(ctx); n != 1 {
		t.Fatalf("count %d", n)
	}
	if err := repo.Delete(ctx, "rule_a"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, "rule_a"); !errors.Is(err, quality.ErrRuleNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestARunAppliesTheCustomRulesAndKeepsTheOnesItUsed(t *testing.T) {
	e := pgEnvWith(t, quality.DefaultSettings(), registry(t), false)
	ctx := context.Background()
	rules := quality.NewPostgresRuleRepository(e.pool)
	if _, err := rules.Create(ctx, quality.CustomRule{
		Rule:    metamodel.QualityRule{ID: "rule_a", Name: "Retention present", Dimension: "validity", Severity: "error", Checks: []metamodel.QualityCondition{{Field: "retention", Op: metamodel.OpAtLeast, Value: 100.0}}},
		Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	svc := quality.NewRunService(fixedSettings{quality.DefaultSettings()}, e.repo, asset.NewPostgresRepository(e.pool, dbRecorder{}), registry(t), quality.WithRuleStore(rules))
	t.Cleanup(svc.Shutdown)

	run, err := svc.StartRun(ctx, quality.TriggerManual, "")
	if err != nil {
		t.Fatal(err)
	}
	var done *quality.Run
	for i := 0; i < 500 && (done == nil || done.Status == quality.RunRunning); i++ {
		done, _ = svc.Run(ctx, run.ID)
		time.Sleep(20 * time.Millisecond)
	}
	if done.Status != quality.RunSucceeded {
		t.Fatalf("%+v", done)
	}
	if len(done.CustomRules) != 1 || done.CustomRules[0].ID != "rule_a" {
		t.Fatalf("the run keeps the rules it applied: %+v", done.CustomRules)
	}
	results, _, err := svc.Results(ctx, run.ID, quality.ResultFilter{})
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	for _, r := range results {
		for _, i := range r.Issues {
			if i.RuleID == "rule_a" {
				hits++
			}
		}
	}
	if hits != 4 || done.Summary.TotalIssues < 5 {
		t.Fatalf("every asset keeps its data less than 100 days: %d findings of the rule, summary %+v", hits, done.Summary)
	}

	// Changing the rule afterwards does not rewrite what the run says it applied.
	if _, err := rules.Update(ctx, quality.CustomRule{Rule: metamodel.QualityRule{ID: "rule_a", Name: "Other", Dimension: "validity", Severity: "warning", Checks: []metamodel.QualityCondition{{Field: "retention", Op: metamodel.OpUnset}}}, Enabled: false}, 1); err != nil {
		t.Fatal(err)
	}
	again, _ := svc.Run(ctx, run.ID)
	if again.CustomRules[0].Name != "Retention present" || !again.CustomRules[0].Enabled {
		t.Fatalf("%+v", again.CustomRules)
	}
}

func TestEvaluateReadsTheAssetsFromTheDatabase(t *testing.T) {
	e := pgEnvWith(t, quality.DefaultSettings(), scoreRegistry(t), false)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO doc_pages (entity_type, entity_id, title, content) VALUES ('asset', 'mrn://table/test/pg0', 'Notes', 'x')`); err != nil {
		t.Fatal(err)
	}
	results, err := e.svc.Evaluate(ctx, []string{"id-pg1", "id-pg0", "id-nope", "id-pg4"})
	if err != nil || len(results) != 3 {
		t.Fatalf("%d %v", len(results), err)
	}
	if results[0].AssetID != "id-pg1" || results[1].AssetID != "id-pg0" || !results[2].Stub {
		t.Fatalf("in the order asked, stubs included and flagged: %+v", results)
	}
	has := func(r quality.AssetResult, dim string) bool {
		for _, d := range r.Dimensions {
			if d == dim {
				return true
			}
		}
		return false
	}
	if !has(results[1], "documentation") || has(results[0], "documentation") {
		t.Fatalf("documentation pages are looked up: %v %v", results[1].Dimensions, results[0].Dimensions)
	}
	if results[0].IssueCount != 1 || results[0].Issues[0].FieldID != "classification" {
		t.Fatalf("%+v", results[0].Issues)
	}
	var recorded int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM quality_runs`).Scan(&recorded); err != nil || recorded != 0 {
		t.Fatalf("an evaluation records nothing: %d %v", recorded, err)
	}
}
