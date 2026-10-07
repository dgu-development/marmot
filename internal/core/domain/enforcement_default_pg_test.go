package domain

import (
	"context"
	"testing"

	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func TestWriteEnforcementDefaultHoldsUntilItIsSet(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool).WithWriteEnforcementDefault(true)

	on, err := repo.WriteEnforced(ctx)
	if err != nil || !on {
		t.Fatalf("default not applied: %v %v", on, err)
	}
	if state, err := repo.EnforcementState(ctx); err != nil || !state.Write || state.UpdatedAt != nil {
		t.Fatalf("state before it is set: %+v %v", state, err)
	}
	if err := repo.SetWriteEnforced(ctx, false, ""); err != nil {
		t.Fatal(err)
	}
	if on, err := repo.WriteEnforced(ctx); err != nil || on {
		t.Fatalf("an explicit choice must win over the default: %v %v", on, err)
	}
	if on, err := NewPostgresRepository(pool).WriteEnforced(ctx); err != nil || on {
		t.Fatalf("off without a default: %v %v", on, err)
	}
}
