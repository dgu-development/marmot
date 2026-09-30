package quality

import (
	"errors"
	"slices"
	"testing"
)

func problems(t *testing.T, s Settings) []string {
	t.Helper()
	var invalid *ValidationError
	if err := s.Validate(); !errors.As(err, &invalid) {
		return nil
	}
	out := make([]string, 0, len(invalid.Fields))
	for _, f := range invalid.Fields {
		out = append(out, f.Field+":"+f.Code)
	}
	return out
}

func TestDefaultsAreValid(t *testing.T) {
	if err := DefaultSettings().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidSettingsAreReportedTogether(t *testing.T) {
	s := DefaultSettings()
	s.Weights = Weights{Completeness: 50, Conformity: 60}
	s.Thresholds = Thresholds{Compliant: 60, Warning: 70}
	s.Schedule = "not a cron"
	s.Retention = Retention{RunDays: 30, ResultDays: 90}
	s.BatchSize = 1
	s.MaxRunSeconds = 5
	delete(s.Rules, RuleRequired)
	s.Rules["bogus"] = RuleSetting{Enabled: true, Severity: SeverityError}
	s.Rules[RuleValidation] = RuleSetting{Enabled: true, Severity: "fatal"}

	got := problems(t, s)
	for _, want := range []string{
		"weights:total", "thresholds:order", "schedule:cron", "retention:order",
		"batch_size:range", "max_run_seconds:range",
		"rules.required:missing_rule", "rules.bogus:unknown_rule", "rules.validation:severity",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
}

func TestRangesAndSchedules(t *testing.T) {
	for name, mutate := range map[string]func(*Settings){
		"weights range":     func(s *Settings) { s.Weights = Weights{Completeness: -1, Conformity: 101} },
		"thresholds range":  func(s *Settings) { s.Thresholds = Thresholds{Compliant: 120, Warning: 10} },
		"retention range":   func(s *Settings) { s.Retention.RunDays = MaxRetentionDays + 1 },
		"batch too large":   func(s *Settings) { s.BatchSize = MaxBatchSize + 1 },
		"timeout too large": func(s *Settings) { s.MaxRunSeconds = MaxRunSeconds + 1 },
	} {
		s := DefaultSettings()
		mutate(&s)
		if len(problems(t, s)) == 0 {
			t.Errorf("%s was accepted", name)
		}
	}
	for _, ok := range []string{"", "0 3 * * *", "*/30 * * * *"} {
		s := DefaultSettings()
		s.Schedule = ok
		if err := s.Validate(); err != nil {
			t.Errorf("schedule %q rejected: %v", ok, err)
		}
	}
	s := DefaultSettings()
	s.Retention = Retention{RunDays: 0, ResultDays: 0}
	if err := s.Validate(); err != nil {
		t.Errorf("no history should be allowed: %v", err)
	}
}
