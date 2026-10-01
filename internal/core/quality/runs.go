package quality

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/rs/zerolog/log"
)

type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
)

const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
)

var (
	ErrRunInProgress    = errors.New("a quality run is already in progress")
	ErrRunNotFound      = errors.New("quality run not found")
	ErrMetamodelOff     = errors.New("the metamodel profile is not loaded")
	errRunInterrupted   = errors.New("interrupted")
	staleAfter          = 5 * time.Minute
	failureWriteTimeout = 10 * time.Second
)

// Run is one pass of the audit over the catalog. Settings is the criterion it used and is only
// filled when a single run is read.
type Run struct {
	ID               string     `json:"id"`
	Trigger          string     `json:"trigger" enums:"manual,schedule"`
	TriggeredBy      string     `json:"triggered_by,omitempty"`
	Status           RunStatus  `json:"status" enums:"running,succeeded,failed"`
	SettingsVersion  int64      `json:"settings_version"`
	MetamodelProfile string     `json:"metamodel_profile,omitempty"`
	MetamodelVersion int        `json:"metamodel_version,omitempty"`
	MetamodelHash    string     `json:"metamodel_hash,omitempty"`
	Processed        int        `json:"processed"`
	Total            int        `json:"total"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	Error            string     `json:"error,omitempty"`
	Summary          *Summary   `json:"summary,omitempty"`
	Settings         *Settings  `json:"settings,omitempty"`
} // @name QualityRun

// ResultFilter narrows and pages the results of a run. Sort is "quality" (weakest first, the default)
// or "name".
type ResultFilter struct {
	Status Status
	Domain string
	Type   string
	Query  string
	Sort   string
	Limit  int
	Offset int
}

const (
	DefaultResultLimit = 50
	MaxResultLimit     = 500
)

// RunRepository is where runs and their results are kept.
type RunRepository interface {
	// Reap fails the runs that stopped reporting progress, so a crash never blocks the next run.
	Reap(ctx context.Context, silentFor time.Duration) error
	// LastStarted is when the latest run started, or the zero time when there has been none.
	LastStarted(ctx context.Context) (time.Time, error)
	// Start records a new run, or returns the one in progress with ErrRunInProgress.
	Start(ctx context.Context, run Run, settings Settings) (*Run, error)
	SetTotal(ctx context.Context, id string, total int) error
	// SaveBatch stores the results of a batch and the progress, in one transaction.
	SaveBatch(ctx context.Context, id string, results []AssetResult, lastAssetID string, processed int) error
	// Finish closes a successful run, keeps issue detail for it alone and prunes what retention drops.
	Finish(ctx context.Context, id string, summary Summary, retention Retention) error
	Fail(ctx context.Context, id string, reason string) error
	Runs(ctx context.Context, limit, offset int) ([]Run, int, error)
	Run(ctx context.Context, id string) (*Run, error)
	Results(ctx context.Context, id string, filter ResultFilter) ([]AssetResult, int, error)
	AssetDomains(ctx context.Context, assetIDs []string) (map[string]string, error)
}

// AssetSource reads the catalog by key, a batch at a time.
type AssetSource interface {
	ListAfter(ctx context.Context, afterID string, limit int) ([]*asset.Asset, error)
	Count(ctx context.Context) (int, error)
}

// RunService starts and reads runs.
type RunService interface {
	// StartRun begins a run in the background and returns it as recorded. A run already in
	// progress returns ErrRunInProgress along with it.
	StartRun(ctx context.Context, trigger, by string) (*Run, error)
	Runs(ctx context.Context, limit, offset int) ([]Run, int, error)
	Run(ctx context.Context, id string) (*Run, error)
	Results(ctx context.Context, id string, filter ResultFilter) ([]AssetResult, int, error)
	// Shutdown cancels the run in progress and waits for it to stop.
	Shutdown()
}

type runService struct {
	settings Service
	repo     RunRepository
	assets   AssetSource
	registry *metamodel.Registry
	now      func() time.Time

	base   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewRunService(settings Service, repo RunRepository, assets AssetSource, registry *metamodel.Registry) RunService {
	base, cancel := context.WithCancel(context.Background())
	return &runService{settings: settings, repo: repo, assets: assets, registry: registry, now: time.Now, base: base, cancel: cancel}
}

func (s *runService) StartRun(ctx context.Context, trigger, by string) (*Run, error) {
	if !s.registry.Enabled() {
		return nil, ErrMetamodelOff
	}
	stored, err := s.settings.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Reap(ctx, staleAfter); err != nil {
		return nil, fmt.Errorf("reaping stale runs: %w", err)
	}
	schema := s.registry.Schema()
	run, err := s.repo.Start(ctx, Run{
		Trigger: trigger, TriggeredBy: by, SettingsVersion: stored.Version,
		MetamodelProfile: schema.ID, MetamodelVersion: schema.Version, MetamodelHash: schema.Hash,
	}, stored.Settings)
	if err != nil {
		return run, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.execute(run.ID, stored.Settings)
	}()
	return run, nil
}

func (s *runService) execute(id string, settings Settings) {
	ctx, cancel := context.WithTimeout(s.base, time.Duration(settings.MaxRunSeconds)*time.Second)
	defer cancel()
	if err := s.audit(ctx, id, settings); err != nil {
		reason := err.Error()
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			reason = "timeout"
		case errors.Is(err, context.Canceled):
			reason = errRunInterrupted.Error()
		}
		log.Error().Err(err).Str("run", id).Msg("Quality run failed")
		failCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), failureWriteTimeout)
		defer stop()
		if ferr := s.repo.Fail(failCtx, id, reason); ferr != nil {
			log.Error().Err(ferr).Str("run", id).Msg("Recording the quality run failure failed")
		}
	}
}

// audit walks the catalog by key a batch at a time, so memory stays bounded to one batch and a
// failure in a batch leaves the earlier ones done.
func (s *runService) audit(ctx context.Context, id string, settings Settings) error {
	total, err := s.assets.Count(ctx)
	if err != nil {
		return err
	}
	if err := s.repo.SetTotal(ctx, id, total); err != nil {
		return err
	}
	auditor := NewAuditor(s.registry, settings, s.now())
	aggregate := NewAggregator(settings.Weights)
	processed, after := 0, ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := s.assets.ListAfter(ctx, after, settings.BatchSize)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		ids := make([]string, len(batch))
		for i, a := range batch {
			ids[i] = a.ID
		}
		domains, err := s.repo.AssetDomains(ctx, ids)
		if err != nil {
			return err
		}
		stored := make([]AssetResult, 0, len(batch))
		for _, a := range batch {
			result := auditor.Audit(a)
			if domain, ok := domains[a.ID]; ok {
				result.DomainID = domain
			}
			aggregate.Add(result)
			if !result.Stub {
				stored = append(stored, result)
			}
		}
		after = batch[len(batch)-1].ID
		processed += len(batch)
		if err := s.repo.SaveBatch(ctx, id, stored, after, processed); err != nil {
			return err
		}
	}
	return s.repo.Finish(ctx, id, aggregate.Summary(), settings.Retention)
}

func (s *runService) Runs(ctx context.Context, limit, offset int) ([]Run, int, error) {
	return s.repo.Runs(ctx, limit, offset)
}

func (s *runService) Run(ctx context.Context, id string) (*Run, error) { return s.repo.Run(ctx, id) }

func (s *runService) Results(ctx context.Context, id string, filter ResultFilter) ([]AssetResult, int, error) {
	if filter.Limit <= 0 {
		filter.Limit = DefaultResultLimit
	}
	filter.Limit = min(filter.Limit, MaxResultLimit)
	filter.Offset = max(filter.Offset, 0)
	if _, err := s.repo.Run(ctx, id); err != nil {
		return nil, 0, err
	}
	return s.repo.Results(ctx, id, filter)
}

func (s *runService) Shutdown() {
	s.cancel()
	s.wg.Wait()
}
