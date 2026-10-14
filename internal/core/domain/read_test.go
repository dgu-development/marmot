package domain_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/auth"
	"github.com/marmotdata/marmot/internal/core/domain"
	"github.com/marmotdata/marmot/internal/core/readscope"
	"github.com/marmotdata/marmot/internal/metrics"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

type silentRecorder struct{ metrics.Recorder }

func (silentRecorder) RecordDBQuery(context.Context, string, time.Duration, bool) {}

func TestARestrictedDomainIsReadOnlyByThoseWithARoleInIt(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	repo := domain.NewPostgresRepository(pool)
	svc := domain.NewService(repo)
	guard := domain.NewGuard(svc, repo, principalFrom)
	api := domain.GuardReads(svc, guard)
	admin := auth.NewOperatorPrincipal()

	finance := mustCreate(t, svc, "Finance", nil)
	payments := mustCreate(t, svc, "Payments", finance)
	cards := mustCreate(t, svc, "Cards", payments)
	legal := mustCreate(t, svc, "Legal", nil)
	yes := true
	if _, err := svc.Update(ctx, finance.ID, domain.UpdateInput{Restricted: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(ctx, domain.UnassignedID, domain.UpdateInput{Restricted: &yes}); !errors.Is(err, domain.ErrProtected) {
		t.Fatalf("Unassigned must stay open: %v", err)
	}

	alice := person(t, pool, "alice") // reader of Payments
	bob := person(t, pool, "bob")     // no role anywhere
	grant, err := svc.GrantRole(ctx, admin, payments.ID, domain.GrantInput{SubjectType: domain.SubjectUser, SubjectID: alice.ID(), Role: domain.RoleReader})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO assets (id, name, mrn, type, providers, created_by) VALUES
		('a-fin', 'ledger', 'mrn://table/pg/ledger', 'Table', '{pg}', 'test'),
		('a-pay', 'payments', 'mrn://table/pg/payments', 'Table', '{pg}', 'test'),
		('a-card', 'cards', 'mrn://table/pg/cards', 'Table', '{pg}', 'test'),
		('a-legal', 'contracts', 'mrn://table/pg/contracts', 'Table', '{pg}', 'test')`); err != nil {
		t.Fatal(err)
	}
	for id, d := range map[string]*domain.Domain{"a-fin": finance, "a-pay": payments, "a-card": cards, "a-legal": legal} {
		if err := svc.Assign(ctx, domain.KindAsset, []string{id}, d.ID); err != nil {
			t.Fatal(err)
		}
	}

	hidden := func(p auth.Principal) []string {
		t.Helper()
		guard.ForgetReads()
		c := ctx
		if p != nil {
			c = as(ctx, p)
		}
		ids, err := guard.Hidden(c)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(ids)
		return ids
	}
	all := []string{finance.ID, payments.ID, cards.ID}
	slices.Sort(all)
	if got := hidden(bob); !slices.Equal(got, all) {
		t.Fatalf("without a role the whole subtree is hidden: %v", got)
	}
	if got := hidden(alice); !slices.Equal(got, []string{finance.ID}) {
		t.Fatalf("a role in Payments reads it and what is under it, not what is above: %v", got)
	}
	if got := hidden(admin); len(got) != 0 {
		t.Fatalf("an administrator reads everything: %v", got)
	}
	if got := hidden(nil); len(got) != 0 {
		t.Fatalf("the server's own work reads everything: %v", got)
	}

	names := func(domains []*domain.Domain, err error) []string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, d := range domains {
			if d.ID != domain.UnassignedID {
				out = append(out, d.Name)
			}
		}
		slices.Sort(out)
		return out
	}
	guard.ForgetReads()
	if got := names(api.Children(as(ctx, bob), nil)); !slices.Equal(got, []string{"Legal"}) {
		t.Fatalf("roots for an outsider: %v", got)
	}
	if _, err := api.Get(as(ctx, bob), finance.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an outsider gets the restricted domain: %v", err)
	}
	if got := names(api.Children(as(ctx, alice), nil)); !slices.Equal(got, []string{"Finance", "Legal"}) {
		t.Fatalf("the domain above a readable one stays in the tree: %v", got)
	}
	if got := names(api.Subtree(as(ctx, alice), finance.ID)); !slices.Equal(got, []string{"Cards", "Finance", "Payments"}) {
		t.Fatalf("subtree for a reader of Payments: %v", got)
	}
	if _, err := api.DomainOf(as(ctx, alice), domain.KindAsset, "a-fin"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the domain of a hidden asset: %v", err)
	}
	if id, err := api.DomainOf(as(ctx, alice), domain.KindAsset, "a-card"); err != nil || id != cards.ID {
		t.Fatalf("the domain of a readable asset: %q %v", id, err)
	}

	readscope.SetResolver(guard.Hidden)
	t.Cleanup(func() { readscope.SetResolver(nil) })
	assets := asset.NewPostgresRepository(pool, silentRecorder{})
	reads := func(p auth.Principal, id string) bool {
		t.Helper()
		_, err := assets.Get(as(ctx, p), id)
		if err != nil && !errors.Is(err, asset.ErrNotFound) {
			t.Fatal(err)
		}
		return err == nil
	}
	if reads(bob, "a-pay") || reads(bob, "a-fin") || !reads(bob, "a-legal") {
		t.Fatal("an outsider reads only the open domain")
	}
	if !reads(alice, "a-pay") || !reads(alice, "a-card") || reads(alice, "a-fin") {
		t.Fatal("a reader of Payments reads it and Cards, not Finance")
	}

	if err := guard.AuthorizeEntities(as(ctx, bob), domain.KindAsset, "a-pay"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("writing what one cannot read, with writes not scoped: %v", err)
	}
	if err := guard.AuthorizeEntities(as(ctx, alice), domain.KindAsset, "a-pay"); err != nil {
		t.Fatalf("writing what one reads is left to the write rules: %v", err)
	}

	told := func(st domain.SubjectType, id, mrn string) bool {
		t.Helper()
		ok, err := guard.SubjectReadsAsset(ctx, st, id, mrn)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !told(domain.SubjectUser, alice.ID(), "mrn://table/pg/cards") || told(domain.SubjectUser, bob.ID(), "mrn://table/pg/cards") ||
		!told(domain.SubjectUser, bob.ID(), "mrn://table/pg/contracts") || told(domain.SubjectUser, alice.ID(), "mrn://table/pg/ledger") {
		t.Fatal("only those with a role are told about an asset of a restricted domain")
	}

	if err := svc.RevokeRole(ctx, admin, payments.ID, grant.ID); err != nil {
		t.Fatal(err)
	}
	if reads(alice, "a-pay") != true {
		t.Fatal("a revocation made elsewhere is seen once the short memory expires, not before")
	}
	guard.ForgetReads()
	if reads(alice, "a-pay") {
		t.Fatal("a revoked role still reads")
	}
}
