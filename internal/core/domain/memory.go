package domain

import (
	"context"

	"github.com/marmotdata/marmot/internal/core/memory"
)

type guardedMemory struct {
	memory.Service
	g *Guard
}

func GuardMemory(inner memory.Service, g *Guard) memory.Service {
	return &guardedMemory{Service: inner, g: g}
}

func (s *guardedMemory) authorize(ctx context.Context, e memory.Entity) error {
	switch e.Type {
	case memory.EntityAsset:
		return s.g.AuthorizeEntities(ctx, KindAsset, e.ID)
	case memory.EntityDataProduct:
		return s.g.AuthorizeEntities(ctx, KindDataProduct, e.ID)
	default:
		return memory.ErrInvalid
	}
}

func (s *guardedMemory) Remember(ctx context.Context, e memory.Entity, in memory.RememberInput) (*memory.Memory, error) {
	if err := s.authorize(ctx, e); err != nil {
		return nil, err
	}
	return s.Service.Remember(ctx, e, in)
}

func (s *guardedMemory) Update(ctx context.Context, e memory.Entity, id string, in memory.UpdateInput) (*memory.Memory, error) {
	if err := s.authorize(ctx, e); err != nil {
		return nil, err
	}
	return s.Service.Update(ctx, e, id, in)
}

func (s *guardedMemory) Forget(ctx context.Context, e memory.Entity, id string) error {
	if err := s.authorize(ctx, e); err != nil {
		return err
	}
	return s.Service.Forget(ctx, e, id)
}
