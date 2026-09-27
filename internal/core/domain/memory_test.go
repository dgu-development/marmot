package domain_test

import (
	"context"
	"errors"
	"testing"

	"github.com/marmotdata/marmot/internal/core/auth"
	"github.com/marmotdata/marmot/internal/core/domain"
	"github.com/marmotdata/marmot/internal/core/memory"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func TestGuardMemoryEnforcesScopeForEveryMutation(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := t.Context()
	repo := domain.NewPostgresRepository(pool)
	domains := domain.NewService(repo)
	finance := mustCreate(t, domains, "Finance", nil)
	legal := mustCreate(t, domains, "Legal", nil)
	operator := auth.NewOperatorPrincipal()
	steward := person(t, pool, "knowledge-steward")
	robotID := seed(t, pool, `INSERT INTO service_accounts(name) VALUES('knowledge-agent') RETURNING id`)
	robot := auth.NewServiceAccountPrincipal(robotID, "knowledge-agent", nil, nil)
	for _, subject := range []struct {
		kind domain.SubjectType
		id   string
	}{{domain.SubjectUser, steward.ID()}, {domain.SubjectServiceAccount, robotID}} {
		if _, err := domains.GrantRole(ctx, operator, finance.ID, domain.GrantInput{SubjectType: subject.kind, SubjectID: subject.id, Role: domain.RoleSteward}); err != nil {
			t.Fatal(err)
		}
	}
	inner := memory.NewService(memory.NewPostgresRepository(pool))
	guarded := domain.GuardMemory(inner, domain.NewGuard(domains, repo, principalFrom))
	author := memory.Author{Type: "user", ID: steward.ID(), Name: "Knowledge steward"}
	for _, kind := range []memory.EntityType{memory.EntityAsset, memory.EntityDataProduct} {
		var entities [2]memory.Entity
		for i, d := range []*domain.Domain{finance, legal} {
			id := ""
			domainKind := domain.KindAsset
			if kind == memory.EntityAsset {
				id = pgtest.SeedAsset(t, pool)
			} else {
				domainKind = domain.KindDataProduct
				if err := pool.QueryRow(ctx, `INSERT INTO data_products(name) VALUES($1) RETURNING id`, d.Name+" product").Scan(&id); err != nil {
					t.Fatal(err)
				}
			}
			if err := domains.Assign(ctx, domainKind, []string{id}, d.ID); err != nil {
				t.Fatal(err)
			}
			entities[i] = memory.Entity{Type: kind, ID: id}
		}
		outside, err := inner.Remember(ctx, entities[1], memory.RememberInput{Content: "Original outside scope", Author: author})
		if err != nil {
			t.Fatal(err)
		}
		if err = repo.SetWriteEnforced(ctx, true, operator.ID()); err != nil {
			t.Fatal(err)
		}
		for _, principal := range []auth.Principal{steward, robot} {
			t.Run(string(kind)+"/"+string(principal.Type()), func(t *testing.T) {
				c := as(ctx, principal)
				m, err := guarded.Remember(c, entities[0], memory.RememberInput{Content: "Inside scope", Author: author})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = guarded.Update(c, entities[0], m.ID, memory.UpdateInput{Content: "Updated inside scope", Author: author}); err != nil {
					t.Fatal(err)
				}
				if err = guarded.Forget(c, entities[0], m.ID); err != nil {
					t.Fatal(err)
				}
				assertMemoryDenied(t, c, guarded, entities[1], outside.ID, author)
			})
		}
		assertMemoryDenied(t, ctx, guarded, entities[0], outside.ID, author)
		assertMemoryDenied(t, ctx, guarded, entities[1], outside.ID, author)
		stored, err := inner.List(ctx, entities[1], memory.ListFilter{})
		if err != nil || stored.Total != 1 || stored.Memories[0].Content != "Original outside scope" {
			t.Fatalf("denied mutation changed memory: %+v %v", stored, err)
		}
	}
}

func assertMemoryDenied(t *testing.T, ctx context.Context, svc memory.Service, e memory.Entity, id string, author memory.Author) {
	t.Helper()
	_, err := svc.Remember(ctx, e, memory.RememberInput{Content: "Must not be saved", Author: author})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("remember outside scope: %v", err)
	}
	_, err = svc.Update(ctx, e, id, memory.UpdateInput{Content: "Must not change", Author: author})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("update outside scope: %v", err)
	}
	if err = svc.Forget(ctx, e, id); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("forget outside scope: %v", err)
	}
}
