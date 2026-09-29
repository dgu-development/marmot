package quality

import (
	"context"
	"errors"
	"fmt"
)

type Service interface {
	// Settings returns the saved settings, or the defaults with version 0.
	Settings(ctx context.Context) (*Stored, error)
	// UpdateSettings validates and saves. expected is the version the caller
	// read; a stale one returns ErrVersionConflict.
	UpdateSettings(ctx context.Context, settings Settings, expected int64, by string) (*Stored, error)
}

type service struct{ repo Repository }

func NewService(repo Repository) Service { return &service{repo: repo} }

func (s *service) Settings(ctx context.Context) (*Stored, error) {
	stored, err := s.repo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading quality settings: %w", err)
	}
	if stored == nil {
		return &Stored{Settings: DefaultSettings()}, nil
	}
	return stored, nil
}

func (s *service) UpdateSettings(ctx context.Context, settings Settings, expected int64, by string) (*Stored, error) {
	if expected < 0 {
		return nil, ErrVersionRequired
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	stored, err := s.repo.Save(ctx, settings, expected, by)
	if err != nil {
		if errors.Is(err, ErrVersionConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("saving quality settings: %w", err)
	}
	return stored, nil
}
