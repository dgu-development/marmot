package quality

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	extquality "github.com/marmotdata/marmot/pkg/extension/quality"
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
	ErrMetamodelOff     = extquality.ErrNoProfile
	errRunInterrupted   = errors.New("interrupted")
	staleAfter          = 5 * time.Minute
	failureWriteTimeout = 10 * time.Second
)

// Run is one pass of the audit over the catalog. Settings is the criterion it used and is only
// filled when a single run is read.
type Run struct {
	ID               string    `json:"id"`
	Trigger          string    `json:"trigger" enums:"manual,schedule"`
	TriggeredBy      string    `json:"triggered_by,omitempty"`
	Status           RunStatus `json:"status" enums:"running,succeeded,failed"`
	SettingsVersion  int64     `json:"settings_version"`
	MetamodelProfile string    `json:"metamodel_profile,omitempty"`
	MetamodelVersion int       `json:"metamodel_version,omitempty"`
	MetamodelHash    string    `json:"metamodel_hash,omitempty"`
	Processed        int       `json:"processed"`
	Total            int       `json:"total"`
	// ScoresWritten, ScoreConflicts and ScoreFailures say how writing the scores on the assets
	// went: written, skipped because the asset was edited after it was read, and failed.
	ScoresWritten  int `json:"scores_written"`
	ScoreConflicts int `json:"score_conflicts"`
	ScoreFailures  int `json:"score_failures"`
	// CustomRules are the rules people wrote that the run applied, as they were when it started;
	// only a single run read carries them.
	CustomRules []CustomRule `json:"custom_rules,omitempty"`
	StartedAt   time.Time    `json:"started_at"`
	FinishedAt  *time.Time   `json:"finished_at,omitempty"`
	Error       string       `json:"error,omitempty"`
	Summary     *Summary     `json:"summary,omitempty"`
	Settings    *Settings    `json:"settings,omitempty"`
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
	// Start records a new run, with the settings and the custom rules it applies, or returns the one
	// in progress with ErrRunInProgress.
	Start(ctx context.Context, run Run, settings Settings, rules []CustomRule) (*Run, error)
	SetTotal(ctx context.Context, id string, total int) error
	// SaveBatch stores the results of a batch and the progress, in one transaction.
	SaveBatch(ctx context.Context, id string, results []AssetResult, lastAssetID string, processed int) error
	// AddScoreCounts adds how a batch of score writes went to the run.
	AddScoreCounts(ctx context.Context, id string, counts ScoreCounts) error
	// Finish closes a successful run, keeps issue detail for it alone and prunes what retention drops.
	Finish(ctx context.Context, id string, summary Summary, retention Retention) error
	Fail(ctx context.Context, id string, reason string) error
	Runs(ctx context.Context, limit, offset int) ([]Run, int, error)
	Run(ctx context.Context, id string) (*Run, error)
	Results(ctx context.Context, id string, filter ResultFilter) ([]AssetResult, int, error)
	AssetDomains(ctx context.Context, assetIDs []string) (map[string]string, error)
	// DocumentedAssets says which of the assets, by MRN, have documentation pages of their own.
	DocumentedAssets(ctx context.Context, mrns []string) (map[string]bool, error)
}

// AssetSource reads the catalog by key, a batch at a time.
type AssetSource interface {
	ListAfter(ctx context.Context, afterID string, limit int) ([]*asset.Asset, error)
	// ListByIDs returns the assets that exist among the ids.
	ListByIDs(ctx context.Context, ids []string) ([]*asset.Asset, error)
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
	// Evaluate judges assets as they are now, with the current settings and rules, without
	// recording anything: it is what a card on an asset's page shows. An id that is not an asset
	// is left out.
	Evaluate(ctx context.Context, ids []string) ([]AssetResult, error)
	// Shutdown cancels the run in progress and waits for it to stop.
	Shutdown()
}

type runService struct {
	settings Service
	repo     RunRepository
	assets   AssetSource
	registry *metamodel.Registry
	writer   ScoreWriter
	rules    RuleRepository
	now      func() time.Time

	base   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// RunOption adjusts a run service.
type RunOption func(*runService)

// WithScoreWriter makes each run write the scores it computes on the assets. Without it a run only
// records its results.
func WithScoreWriter(writer ScoreWriter) RunOption {
	return func(s *runService) { s.writer = writer }
}

// WithRuleStore makes runs apply the custom rules kept there as well as the profile's.
func WithRuleStore(rules RuleRepository) RunOption {
	return func(s *runService) { s.rules = rules }
}

func NewRunService(settings Service, repo RunRepository, assets AssetSource, registry *metamodel.Registry, options ...RunOption) RunService {
	base, cancel := context.WithCancel(context.Background())
	s := &runService{settings: settings, repo: repo, assets: assets, registry: registry, now: time.Now, base: base, cancel: cancel}
	for _, option := range options {
		option(s)
	}
	return s
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
	custom, err := s.customRules(ctx)
	if err != nil {
		return nil, err
	}
	schema := s.registry.Schema()
	run, err := s.repo.Start(ctx, Run{
		Trigger: trigger, TriggeredBy: by, SettingsVersion: stored.Version,
		MetamodelProfile: schema.ID, MetamodelVersion: schema.Version, MetamodelHash: schema.Hash,
	}, stored.Settings, custom)
	if err != nil {
		return run, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.execute(run.ID, stored.Settings, custom)
	}()
	return run, nil
}

func (s *runService) customRules(ctx context.Context) ([]CustomRule, error) {
	if s.rules == nil {
		return nil, nil
	}
	rules, err := s.rules.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading custom quality rules: %w", err)
	}
	return rules, nil
}

func (s *runService) execute(id string, settings Settings, custom []CustomRule) {
	ctx, cancel := context.WithTimeout(s.base, time.Duration(settings.MaxRunSeconds)*time.Second)
	defer cancel()
	if err := s.audit(ctx, id, settings, custom); err != nil {
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
func (s *runService) audit(ctx context.Context, id string, settings Settings, custom []CustomRule) error {
	total, err := s.assets.Count(ctx)
	if err != nil {
		return err
	}
	if err := s.repo.SetTotal(ctx, id, total); err != nil {
		return err
	}
	auditor := NewAuditor(s.registry, settings, s.now(), WithCustomRules(custom))
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
		mrns := make([]string, 0, len(batch))
		for _, a := range batch {
			if a.MRN != nil {
				mrns = append(mrns, *a.MRN)
			}
		}
		documented, err := s.repo.DocumentedAssets(ctx, mrns)
		if err != nil {
			return err
		}
		stored := make([]AssetResult, 0, len(batch))
		results := make([]AssetResult, len(batch))
		for i, a := range batch {
			result := auditor.AuditWithDocs(a, a.MRN != nil && documented[*a.MRN])
			if domain, ok := domains[a.ID]; ok {
				result.DomainID = domain
			}
			results[i] = result
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
		if s.writer != nil {
			counts, err := publish(ctx, s.writer, auditor, batch, results)
			if err := firstError(err, s.repo.AddScoreCounts(context.WithoutCancel(ctx), id, counts)); err != nil {
				return err
			}
		}
	}
	return s.repo.Finish(ctx, id, aggregate.Summary(), settings.Retention)
}

func firstError(first, second error) error {
	if first != nil {
		return first
	}
	return second
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

// MaxEvaluated bounds how many assets one evaluation judges.
const MaxEvaluated = 200

func (s *runService) Evaluate(ctx context.Context, ids []string) ([]AssetResult, error) {
	if !s.registry.Enabled() {
		return nil, ErrMetamodelOff
	}
	if len(ids) > MaxEvaluated {
		ids = ids[:MaxEvaluated]
	}
	stored, err := s.settings.Settings(ctx)
	if err != nil {
		return nil, err
	}
	custom, err := s.customRules(ctx)
	if err != nil {
		return nil, err
	}
	assets, err := s.assets.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	mrns := make([]string, 0, len(assets))
	for _, a := range assets {
		if a.MRN != nil {
			mrns = append(mrns, *a.MRN)
		}
	}
	documented, err := s.repo.DocumentedAssets(ctx, mrns)
	if err != nil {
		return nil, err
	}
	domains, err := s.repo.AssetDomains(ctx, ids)
	if err != nil {
		return nil, err
	}
	auditor := NewAuditor(s.registry, stored.Settings, s.now(), WithCustomRules(custom))
	byID := make(map[string]AssetResult, len(assets))
	for _, a := range assets {
		result := auditor.AuditWithDocs(a, a.MRN != nil && documented[*a.MRN])
		if domain, ok := domains[a.ID]; ok {
			result.DomainID = domain
		}
		byID[a.ID] = result
	}
	out := make([]AssetResult, 0, len(byID))
	for _, id := range ids {
		if result, ok := byID[id]; ok {
			out = append(out, result)
		}
	}
	return out, nil
}

func (s *runService) Shutdown() {
	s.cancel()
	s.wg.Wait()
}
