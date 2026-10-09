package glossary

import (
	"context"
	"errors"
	"testing"

	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func TestSiblingsCannotShareANameButCousinsCan(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx, "INSERT INTO users (username, name) VALUES ('ana', 'Ana') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	repo := NewPostgresRepository(pool, noopRecorder{})
	svc := NewService(repo)
	owners := []OwnerInput{{ID: userID, Type: "user"}}
	create := func(name string, parent *string) (*GlossaryTerm, error) {
		return svc.Create(ctx, CreateTermInput{Name: name, Definition: "d", ParentTermID: parent, Owners: owners})
	}

	root, err := create("Cliente", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := create("CLIENTE", nil); !errors.Is(err, ErrTermExists) {
		t.Fatalf("a root term with the same name in another case: %v", err)
	}
	child, err := create("Cliente", &root.ID)
	if err != nil {
		t.Fatalf("the same name under another parent is another term: %v", err)
	}
	if _, err := create("cliente", &root.ID); !errors.Is(err, ErrTermExists) {
		t.Fatalf("a sibling with the same name: %v", err)
	}

	other, err := create("Proveedor", nil)
	if err != nil {
		t.Fatal(err)
	}
	taken := "cliente"
	if _, err := svc.Update(ctx, other.ID, UpdateTermInput{Name: &taken}); !errors.Is(err, ErrTermExists) {
		t.Fatalf("renaming onto a sibling: %v", err)
	}

	found, err := repo.GetByName(ctx, "CLIENTE")
	if err != nil || found.ID != root.ID {
		t.Fatalf("an ingestion finds the oldest whatever the case: %+v, %v", found, err)
	}

	if err := svc.Delete(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := create("Cliente", &root.ID); err != nil {
		t.Fatalf("a deleted term frees its name: %v", err)
	}
}
