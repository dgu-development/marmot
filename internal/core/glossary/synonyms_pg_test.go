package glossary

import (
	"context"
	"testing"

	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func TestSearchFindsATermBySynonym(t *testing.T) {
	pool := pgtest.TempDB(t)
	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx, "INSERT INTO users (username, name) VALUES ('ana', 'Ana') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	svc := NewService(NewPostgresRepository(pool, noopRecorder{}))
	owners := []OwnerInput{{ID: userID, Type: "user"}}

	customer, err := svc.Create(ctx, CreateTermInput{
		Name: "Customer", Definition: "Someone who buys", Owners: owners,
		Metadata: map[string]interface{}{"synonyms": []interface{}{"Client", "Purchaser"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, CreateTermInput{Name: "Invoice", Definition: "A bill sent to a client", Owners: owners}); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"purchaser", "Client"} {
		result, err := svc.Search(ctx, SearchFilter{Query: query, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if !hasTerm(result.Terms, "Customer") {
			t.Errorf("searching %q misses the term with that synonym: %v", query, names(result.Terms))
		}
	}
	// A term without tags used to get no full-text vector, so only its name matched.
	result, err := svc.Search(ctx, SearchFilter{Query: "bill", Limit: 10})
	if err != nil || !hasTerm(result.Terms, "Invoice") {
		t.Fatalf("searching a word of the definition of a term without tags: %v, %v", result, err)
	}

	var indexed bool
	if err := pool.QueryRow(ctx,
		`SELECT search_text @@ websearch_to_tsquery('english', 'purchaser') FROM search_index WHERE type = 'glossary' AND entity_id = $1`,
		customer.ID).Scan(&indexed); err != nil || !indexed {
		t.Fatalf("global search index misses the synonym: %v, %v", indexed, err)
	}
}

func hasTerm(terms []*GlossaryTerm, name string) bool {
	for _, term := range terms {
		if term.Name == name {
			return true
		}
	}
	return false
}

func names(terms []*GlossaryTerm) []string {
	out := make([]string, len(terms))
	for i, term := range terms {
		out[i] = term.Name
	}
	return out
}
