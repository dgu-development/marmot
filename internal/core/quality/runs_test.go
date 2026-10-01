package quality_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/quality"
)

type fixedSettings struct{ settings quality.Settings }

func (f fixedSettings) Settings(context.Context) (*quality.Stored, error) {
	return &quality.Stored{Settings: f.settings, Version: 3}, nil
}

func (fixedSettings) UpdateSettings(context.Context, quality.Settings, int64, string) (*quality.Stored, error) {
	return nil, errors.New("not used")
}

type memorySource struct {
	assets []*asset.Asset
	limits []int
	block  chan struct{}
	err    error
}

func (m *memorySource) Count(context.Context) (int, error) { return len(m.assets), nil }

func (m *memorySource) ListAfter(ctx context.Context, after string, limit int) ([]*asset.Asset, error) {
	m.limits = append(m.limits, limit)
	if m.block != nil {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if m.err != nil {
		return nil, m.err
	}
	var out []*asset.Asset
	for _, a := range m.assets {
		if a.ID > after && len(out) < limit {
			out = append(out, a)
		}
	}
	return out, nil
}

type memoryRuns struct {
	mu          sync.Mutex
	running     *quality.Run
	batches     [][]quality.AssetResult
	progress    []int
	total       int
	summary     *quality.Summary
	retention   quality.Retention
	failure     string
	done        chan struct{}
	domains     map[string]string
	lastStarted time.Time
	counts      quality.ScoreCounts
}

func newMemoryRuns() *memoryRuns {
	return &memoryRuns{done: make(chan struct{}), domains: map[string]string{}}
}

func (m *memoryRuns) Reap(context.Context, time.Duration) error { return nil }

func (m *memoryRuns) LastStarted(context.Context) (time.Time, error) { return m.lastStarted, nil }

func (m *memoryRuns) Start(_ context.Context, run quality.Run, _ quality.Settings) (*quality.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running != nil {
		return m.running, quality.ErrRunInProgress
	}
	run.ID, run.Status = "run-1", quality.RunRunning
	m.running = &run
	return &run, nil
}

func (m *memoryRuns) SetTotal(_ context.Context, _ string, total int) error {
	m.total = total
	return nil
}

func (m *memoryRuns) SaveBatch(_ context.Context, _ string, results []quality.AssetResult, _ string, processed int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batches = append(m.batches, results)
	m.progress = append(m.progress, processed)
	return nil
}

func (m *memoryRuns) AddScoreCounts(_ context.Context, _ string, counts quality.ScoreCounts) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts.Written += counts.Written
	m.counts.Conflicts += counts.Conflicts
	m.counts.Failed += counts.Failed
	return nil
}

func (m *memoryRuns) Finish(_ context.Context, _ string, summary quality.Summary, retention quality.Retention) error {
	m.summary, m.retention = &summary, retention
	close(m.done)
	return nil
}

func (m *memoryRuns) Fail(_ context.Context, _ string, reason string) error {
	m.failure = reason
	close(m.done)
	return nil
}

func (m *memoryRuns) Runs(context.Context, int, int) ([]quality.Run, int, error) { return nil, 0, nil }
func (m *memoryRuns) Run(context.Context, string) (*quality.Run, error) {
	return nil, quality.ErrRunNotFound
}
func (m *memoryRuns) Results(context.Context, string, quality.ResultFilter) ([]quality.AssetResult, int, error) {
	return nil, 0, nil
}

func (m *memoryRuns) AssetDomains(_ context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if d, ok := m.domains[id]; ok {
			out[id] = d
		}
	}
	return out, nil
}

func wait(t *testing.T, m *memoryRuns) {
	t.Helper()
	select {
	case <-m.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the run did not finish")
	}
}

func catalog(n int) []*asset.Asset {
	var out []*asset.Asset
	for i := 0; i < n; i++ {
		a := newAsset(fmt.Sprintf("a%02d", i), complete())
		out = append(out, a)
	}
	return out
}

func service(t *testing.T, settings quality.Settings, source *memorySource, repo *memoryRuns, reg *metamodel.Registry) quality.RunService {
	t.Helper()
	svc := quality.NewRunService(fixedSettings{settings}, repo, source, reg)
	t.Cleanup(svc.Shutdown)
	return svc
}

func TestARunWalksTheCatalogInBatchesAndSavesTheProgress(t *testing.T) {
	settings := quality.DefaultSettings()
	settings.BatchSize = 2
	source := &memorySource{assets: catalog(5)}
	source.assets[1].IsStub = true
	repo := newMemoryRuns()
	repo.domains["id-a03"] = "dom-1"
	svc := service(t, settings, source, repo, registry(t))

	run, err := svc.StartRun(context.Background(), quality.TriggerManual, "user-1")
	if err != nil || run.Status != quality.RunRunning || run.SettingsVersion != 3 || run.MetamodelProfile != "audit" {
		t.Fatalf("run = %+v, %v", run, err)
	}
	wait(t, repo)

	if repo.failure != "" {
		t.Fatalf("failed: %s", repo.failure)
	}
	if repo.total != 5 || fmt.Sprint(repo.progress) != "[2 4 5]" {
		t.Fatalf("total %d, progress %v", repo.total, repo.progress)
	}
	for _, limit := range source.limits {
		if limit != 2 {
			t.Fatalf("limits = %v", source.limits)
		}
	}
	stored := 0
	for _, batch := range repo.batches {
		for _, result := range batch {
			stored++
			if result.Stub {
				t.Fatal("a stub was stored")
			}
		}
	}
	if stored != 4 {
		t.Fatalf("stored %d results, want the 4 that are not stubs", stored)
	}
	if repo.summary.TotalAssets != 4 || repo.summary.Stubs != 1 || repo.summary.Quality != 100 {
		t.Fatalf("summary = %+v", repo.summary)
	}
	if len(repo.summary.ByDomain) != 2 {
		t.Fatalf("by domain = %+v", repo.summary.ByDomain)
	}
	if repo.retention != settings.Retention {
		t.Fatalf("retention = %+v", repo.retention)
	}
}

func TestASecondRunWhileOneIsInProgressIsRefusedAndNamesTheFirst(t *testing.T) {
	source := &memorySource{assets: catalog(1), block: make(chan struct{})}
	repo := newMemoryRuns()
	svc := service(t, quality.DefaultSettings(), source, repo, registry(t))

	first, err := svc.StartRun(context.Background(), quality.TriggerManual, "a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.StartRun(context.Background(), quality.TriggerManual, "b")
	if !errors.Is(err, quality.ErrRunInProgress) || second == nil || second.ID != first.ID {
		t.Fatalf("second = %+v, %v", second, err)
	}
}

func TestStoppingTheServerEndsTheRunAsInterrupted(t *testing.T) {
	source := &memorySource{assets: catalog(1), block: make(chan struct{})}
	repo := newMemoryRuns()
	svc := service(t, quality.DefaultSettings(), source, repo, registry(t))
	if _, err := svc.StartRun(context.Background(), quality.TriggerManual, ""); err != nil {
		t.Fatal(err)
	}
	svc.Shutdown()
	wait(t, repo)
	if repo.failure != "interrupted" {
		t.Fatalf("failure = %q", repo.failure)
	}
}

func TestARunPastItsTimeLimitFailsWithTimeout(t *testing.T) {
	settings := quality.DefaultSettings()
	settings.MaxRunSeconds = 0
	source := &memorySource{assets: catalog(1), block: make(chan struct{})}
	repo := newMemoryRuns()
	svc := service(t, settings, source, repo, registry(t))
	if _, err := svc.StartRun(context.Background(), quality.TriggerManual, ""); err != nil {
		t.Fatal(err)
	}
	wait(t, repo)
	if repo.failure != "timeout" {
		t.Fatalf("failure = %q", repo.failure)
	}
}

func TestAReadFailureLeavesTheRunFailedWithItsReason(t *testing.T) {
	source := &memorySource{assets: catalog(1), err: errors.New("database gone")}
	repo := newMemoryRuns()
	svc := service(t, quality.DefaultSettings(), source, repo, registry(t))
	if _, err := svc.StartRun(context.Background(), quality.TriggerManual, ""); err != nil {
		t.Fatal(err)
	}
	wait(t, repo)
	if repo.failure != "database gone" {
		t.Fatalf("failure = %q", repo.failure)
	}
}

func TestWithoutAMetamodelProfileThereIsNothingToAuditAgainst(t *testing.T) {
	svc := service(t, quality.DefaultSettings(), &memorySource{}, newMemoryRuns(), metamodel.Native())
	if _, err := svc.StartRun(context.Background(), quality.TriggerManual, ""); !errors.Is(err, quality.ErrMetamodelOff) {
		t.Fatalf("err = %v", err)
	}
}

func TestResultPagesAreBounded(t *testing.T) {
	svc := service(t, quality.DefaultSettings(), &memorySource{}, newMemoryRuns(), registry(t))
	if _, _, err := svc.Results(context.Background(), "missing", quality.ResultFilter{Limit: 100000}); !errors.Is(err, quality.ErrRunNotFound) {
		t.Fatalf("err = %v", err)
	}
}
