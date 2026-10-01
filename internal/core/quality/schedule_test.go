package quality_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/marmotdata/marmot/internal/core/quality"
)

func at(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

// The zone of a schedule without one is the server's, as for the ingestion schedules.
func serverIn(t *testing.T, zone *time.Location) {
	t.Helper()
	previous := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = previous })
}

func TestAScheduleIsReadInTheServersZoneUnlessItNamesItsOwn(t *testing.T) {
	serverIn(t, time.UTC)
	cases := map[string]struct{ expr, want string }{
		"server zone":  {"0 3 * * *", "2026-10-02T03:00:00Z"},
		"its own zone": {"CRON_TZ=Europe/Madrid 0 3 * * *", "2026-10-02T01:00:00Z"},
		"every hour":   {"0 * * * *", "2026-10-01T13:00:00Z"},
	}
	for name, c := range cases {
		next, scheduled, err := quality.NextRun(c.expr, at("2026-10-01T12:00:00Z"))
		if err != nil || !scheduled || !next.Equal(at(c.want)) {
			t.Errorf("%s: %v %v %v, want %s", name, next, scheduled, err, c.want)
		}
	}
	if _, scheduled, err := quality.NextRun("  ", at("2026-10-01T12:00:00Z")); scheduled || err != nil {
		t.Errorf("empty means manual only: %v %v", scheduled, err)
	}
	for _, bad := range []string{"every day", "61 * * * *", "0 3 * *", "CRON_TZ=Nowhere/Land 0 3 * * *"} {
		if _, err := quality.ParseSchedule(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

type recordedRuns struct {
	started []string
	err     error
}

func (r *recordedRuns) StartRun(_ context.Context, trigger, by string) (*quality.Run, error) {
	r.started = append(r.started, trigger+":"+by)
	return &quality.Run{ID: "r"}, r.err
}

func (*recordedRuns) Runs(context.Context, int, int) ([]quality.Run, int, error) { return nil, 0, nil }
func (*recordedRuns) Run(context.Context, string) (*quality.Run, error)          { return nil, nil }
func (*recordedRuns) Results(context.Context, string, quality.ResultFilter) ([]quality.AssetResult, int, error) {
	return nil, 0, nil
}
func (*recordedRuns) Shutdown() {}

type scheduleSettings struct{ stored quality.Stored }

func (s scheduleSettings) Settings(context.Context) (*quality.Stored, error) { return &s.stored, nil }
func (scheduleSettings) UpdateSettings(context.Context, quality.Settings, int64, string) (*quality.Stored, error) {
	return nil, errors.New("not used")
}

type lastRun struct{ at time.Time }

func (l lastRun) LastStarted(context.Context) (time.Time, error) { return l.at, nil }

func tick(t *testing.T, schedule string, updated, last, now string, runs *recordedRuns) *quality.Scheduler {
	t.Helper()
	serverIn(t, time.UTC)
	stored := quality.Stored{Settings: quality.DefaultSettings(), Version: 1}
	stored.Schedule = schedule
	if updated != "" {
		stored.UpdatedAt = at(updated)
	}
	var lastAt time.Time
	if last != "" {
		lastAt = at(last)
	}
	scheduler := quality.NewScheduler(scheduleSettings{stored}, runs, lastRun{lastAt})
	quality.SetSchedulerClock(scheduler, func() time.Time { return at(now) })
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	return scheduler
}

func TestADueSlotStartsAScheduledRunOnce(t *testing.T) {
	runs := &recordedRuns{}
	tick(t, "0 3 * * *", "2026-09-20T10:00:00Z", "2026-09-30T03:00:05Z", "2026-10-01T03:00:10Z", runs)
	if len(runs.started) != 1 || runs.started[0] != "schedule:" {
		t.Fatalf("started = %v", runs.started)
	}

	again := &recordedRuns{}
	tick(t, "0 3 * * *", "2026-09-20T10:00:00Z", "2026-10-01T03:00:12Z", "2026-10-01T03:00:40Z", again)
	if len(again.started) != 0 {
		t.Fatalf("the run that just started must not start another: %v", again.started)
	}
}

func TestNothingStartsBeforeItsTimeOrWithoutASchedule(t *testing.T) {
	for name, c := range map[string][]string{
		"before the slot":        {"0 3 * * *", "2026-09-20T10:00:00Z", "2026-09-30T03:00:05Z", "2026-10-01T02:59:50Z"},
		"no schedule":            {"", "2026-09-20T10:00:00Z", "", "2026-10-01T03:00:10Z"},
		"a manual run after it":  {"0 3 * * *", "2026-09-20T10:00:00Z", "2026-10-01T03:30:00Z", "2026-10-01T04:00:00Z"},
		"saved after the slot":   {"0 3 * * *", "2026-10-01T03:30:00Z", "", "2026-10-01T04:00:00Z"},
		"an expression gone bad": {"nonsense", "2026-09-20T10:00:00Z", "", "2026-10-01T03:00:10Z"},
	} {
		runs := &recordedRuns{}
		tick(t, c[0], c[1], c[2], c[3], runs)
		if len(runs.started) != 0 {
			t.Errorf("%s: started %v", name, runs.started)
		}
	}
}

func TestAMissedSlotFiresOnceAndARunInProgressIsNotAnError(t *testing.T) {
	runs := &recordedRuns{}
	tick(t, "0 * * * *", "2026-09-20T10:00:00Z", "2026-10-01T03:00:00Z", "2026-10-01T09:30:00Z", runs)
	if len(runs.started) != 1 {
		t.Fatalf("six missed slots start one run: %v", runs.started)
	}

	busy := &recordedRuns{err: quality.ErrRunInProgress}
	tick(t, "0 3 * * *", "2026-09-20T10:00:00Z", "2026-09-30T03:00:05Z", "2026-10-01T03:00:10Z", busy)
	off := &recordedRuns{err: quality.ErrMetamodelOff}
	tick(t, "0 3 * * *", "2026-09-20T10:00:00Z", "2026-09-30T03:00:05Z", "2026-10-01T03:00:10Z", off)
}

func TestNextRunIsReportedAndAnOverdueOneIsNow(t *testing.T) {
	serverIn(t, time.UTC)
	stored := quality.Stored{Settings: quality.DefaultSettings()}
	stored.Schedule = "0 3 * * *"
	stored.UpdatedAt = at("2026-09-20T10:00:00Z")
	scheduler := quality.NewScheduler(scheduleSettings{stored}, &recordedRuns{}, lastRun{at("2026-10-01T03:00:05Z")})
	quality.SetSchedulerClock(scheduler, func() time.Time { return at("2026-10-01T12:00:00Z") })
	next := scheduler.Next(context.Background(), &stored)
	if next == nil || !next.Equal(at("2026-10-02T03:00:00Z")) {
		t.Fatalf("next = %v", next)
	}

	overdue := quality.NewScheduler(scheduleSettings{stored}, &recordedRuns{}, lastRun{at("2026-09-30T03:00:05Z")})
	quality.SetSchedulerClock(overdue, func() time.Time { return at("2026-10-01T12:00:00Z") })
	if got := overdue.Next(context.Background(), &stored); got == nil || !got.Equal(at("2026-10-01T12:00:00Z")) {
		t.Fatalf("overdue = %v", got)
	}

	stored.Schedule = ""
	if overdue.Next(context.Background(), &stored) != nil {
		t.Fatal("no schedule, no next run")
	}
}

func TestAScheduleWithoutAZoneFollowsTheServerAsPipelinesDo(t *testing.T) {
	serverIn(t, time.FixedZone("CEST", 2*3600))
	next, _, err := quality.NextRun("0 3 * * *", at("2026-10-01T12:00:00Z"))
	if err != nil || !next.Equal(at("2026-10-02T01:00:00Z")) {
		t.Fatalf("03:00 server time is 01:00Z: %v %v", next, err)
	}
}
