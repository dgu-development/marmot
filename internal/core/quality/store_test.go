package quality_test

import (
	"context"
	"errors"
	"testing"

	"github.com/marmotdata/marmot/internal/core/quality"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func newService(t *testing.T) quality.Service {
	t.Helper()
	return quality.NewService(quality.NewPostgresRepository(pgtest.TempDB(t)))
}

func TestUnsavedSettingsAreTheDefaultsAtVersionZero(t *testing.T) {
	stored, err := newService(t).Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version != 0 || stored.BatchSize != quality.DefaultSettings().BatchSize {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestSettingsAreSavedWithCompareAndSet(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	s := quality.DefaultSettings()

	first, err := svc.UpdateSettings(ctx, s, 0, "")
	if err != nil || first.Version != 1 {
		t.Fatalf("first = %+v, %v", first, err)
	}
	if _, err := svc.UpdateSettings(ctx, s, 0, ""); !errors.Is(err, quality.ErrVersionConflict) {
		t.Fatalf("a second first save: %v", err)
	}

	s.Thresholds = quality.Thresholds{Compliant: 95, Warning: 80}
	second, err := svc.UpdateSettings(ctx, s, 1, "")
	if err != nil || second.Version != 2 || second.Thresholds.Compliant != 95 {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if _, err := svc.UpdateSettings(ctx, s, 1, ""); !errors.Is(err, quality.ErrVersionConflict) {
		t.Fatalf("a stale save: %v", err)
	}
	if _, err := svc.UpdateSettings(ctx, s, -1, ""); !errors.Is(err, quality.ErrVersionRequired) {
		t.Fatalf("no version: %v", err)
	}

	got, err := svc.Settings(ctx)
	if err != nil || got.Version != 2 || got.Thresholds.Warning != 80 {
		t.Fatalf("got = %+v, %v", got, err)
	}
}

func TestInvalidSettingsAreNotSaved(t *testing.T) {
	svc := newService(t)
	s := quality.DefaultSettings()
	s.BatchSize = 1
	var invalid *quality.ValidationError
	if _, err := svc.UpdateSettings(context.Background(), s, 0, ""); !errors.As(err, &invalid) {
		t.Fatalf("err = %v", err)
	}
	if stored, _ := svc.Settings(context.Background()); stored.Version != 0 {
		t.Fatalf("saved anyway: %+v", stored)
	}
}
