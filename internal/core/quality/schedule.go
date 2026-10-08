package quality

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
)

// ScheduleCheckInterval is how often the scheduler looks at the clock: a five-field cron has a
// resolution of one minute, so a run starts within this long of its time. Like the ingestion
// schedules it runs on a singleton task, so one replica at a time checks.
const ScheduleCheckInterval = 30 * time.Second

// NextRun is when an expression fires next after t; false when it is empty.
func NextRun(expression string, after time.Time) (time.Time, bool, error) {
	schedule, err := ParseSchedule(expression)
	if err != nil || schedule == nil {
		return time.Time{}, false, err
	}
	return schedule.Next(after.In(time.Local)), true, nil
}

// LastRunSource tells when the audit last started, however it was started.
type LastRunSource interface {
	LastStarted(ctx context.Context) (time.Time, error)
}

// Scheduler starts the audit when the schedule of the settings says it is due. It holds no state of
// its own: what is due follows from the settings and from the runs, so several replicas and a
// restart agree on it.
type Scheduler struct {
	settings Service
	runs     RunService
	last     LastRunSource
	now      func() time.Time
}

func NewScheduler(settings Service, runs RunService, last LastRunSource) *Scheduler {
	return &Scheduler{settings: settings, runs: runs, last: last, now: time.Now}
}

// due is the next firing after the later of the last run and the last time the settings changed.
// Counting the settings makes a freshly saved schedule wait for its next slot instead of firing for
// one that passed before it existed, and counting any run, a manual one included, avoids running
// twice for one slot. A slot missed while the server was down fires once, not once per slot.
func (s *Scheduler) due(ctx context.Context, stored *Stored) (next time.Time, scheduled bool, err error) {
	schedule, err := ParseSchedule(stored.Schedule)
	if err != nil || schedule == nil {
		return time.Time{}, false, err
	}
	last, err := s.last.LastStarted(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	reference := stored.UpdatedAt
	if last.After(reference) {
		reference = last
	}
	return schedule.Next(reference.In(time.Local)), true, nil
}

// Next is when the next scheduled run will start, or nil when there is no schedule. A run that is
// already due starts at the next check, so it is reported as now.
func (s *Scheduler) Next(ctx context.Context, stored *Stored) *time.Time {
	next, scheduled, err := s.due(ctx, stored)
	if err != nil || !scheduled {
		return nil
	}
	if now := s.now(); next.Before(now) {
		next = now
	}
	return &next
}

// Tick is the body of the periodic task.
func (s *Scheduler) Tick(ctx context.Context) error {
	stored, err := s.settings.Settings(ctx)
	if err != nil {
		return err
	}
	next, scheduled, err := s.due(ctx, stored)
	if err != nil {
		log.Warn().Err(err).Str("schedule", stored.Schedule).Msg("Quality schedule could not be read")
		return nil
	}
	if !scheduled || next.After(s.now()) {
		return nil
	}
	_, err = s.runs.StartRun(ctx, TriggerSchedule, "")
	switch {
	case errors.Is(err, ErrRunInProgress):
		return nil
	case errors.Is(err, ErrMetamodelOff):
		log.Warn().Msg("Scheduled quality run skipped: the metamodel profile is not loaded")
		return nil
	case err != nil:
		return err
	}
	log.Info().Time("slot", next).Msg("Scheduled quality run started")
	return nil
}
