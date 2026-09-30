package glossary

import (
	"context"
	"testing"

	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func TestGlossarySearchIgnoresAccents(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx, "INSERT INTO users (username, name) VALUES ('ana', 'Ana') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	svc := NewService(NewPostgresRepository(pool, noopRecorder{}))
	owners := []OwnerInput{{ID: userID, Type: "user"}}
	if _, err := svc.Create(ctx, CreateTermInput{Name: "Clasificación", Definition: "Nivel de sensibilidad de los datos", Owners: owners}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, CreateTermInput{Name: "Invoice", Definition: "A bill", Owners: owners}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"clasificacion", "CLASIFICACIÓN", "sensibilidad", "nivel sensibilídad", "clasif"} {
		result, err := svc.Search(ctx, SearchFilter{Query: query, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if !hasTerm(result.Terms, "Clasificación") {
			t.Errorf("%q misses the accented term: %v", query, names(result.Terms))
		}
	}
	result, err := svc.Search(ctx, SearchFilter{Query: "bill", Limit: 10})
	if err != nil || !hasTerm(result.Terms, "Invoice") {
		t.Errorf("an English term stopped matching: %v, %v", result, err)
	}
}
