package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/core/knowledge"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func TestKnowledgeWorkerPublishesCatalogWithoutMemoryAndRefreshesIt(t *testing.T) {
	pool := pgtest.TempDB(t)
	id := pgtest.SeedAsset(t, pool)
	svc := knowledge.NewService(pool, knowledge.Options{})
	entity := knowledge.Entity{Kind: "asset", ID: id}
	compileKnowledge(t.Context(), pool, svc)
	page, err := svc.Get(t.Context(), entity)
	if err != nil || page.PublishedAt == nil || page.Freshness != "fresh" {
		t.Fatalf("catalog was not published without memories: %+v %v", page, err)
	}
	oldHash := page.SourceHash
	if _, err := pool.Exec(t.Context(), `UPDATE assets SET description='Updated catalog relationship' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	compileKnowledge(t.Context(), pool, svc)
	page, err = svc.Get(t.Context(), entity)
	if err != nil || page.Freshness != "fresh" || page.SourceHash == oldHash || !strings.Contains(page.Content, "Updated catalog relationship") {
		t.Fatalf("changed catalog was not republished: %+v %v", page, err)
	}
	var productID string
	if err := pool.QueryRow(t.Context(), `INSERT INTO data_products(name) VALUES('Orders product') RETURNING id`).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO data_product_memberships(data_product_id,asset_id,source) VALUES($1,$2,'manual')`, productID, id); err != nil {
		t.Fatal(err)
	}
	compileKnowledge(t.Context(), pool, svc)
	page, err = svc.Get(t.Context(), entity)
	if err != nil || page.Freshness != "fresh" || !strings.Contains(page.Content, "Orders product") {
		t.Fatalf("ingested relation was not published: %+v %v", page, err)
	}
}

func TestKnowledgeWorkerKeepsLLMSynthesisForReview(t *testing.T) {
	pool := pgtest.TempDB(t)
	id := pgtest.SeedAsset(t, pool)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var evidence []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &evidence); err != nil {
			t.Error(err)
			return
		}
		content, _ := json.Marshal(map[string]any{"sections": []any{map[string]any{"text": "A grounded claim", "source_ids": []string{evidence[0].ID}}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}, "finish_reason": "stop"}}})
	}))
	defer provider.Close()
	svc := knowledge.NewService(pool, knowledge.Options{Endpoint: provider.URL, Model: "test"})
	compileKnowledge(t.Context(), pool, svc)
	page, err := svc.Get(t.Context(), knowledge.Entity{Kind: "asset", ID: id})
	if err != nil || page.PublishedAt != nil || page.DraftMode != "llm" || page.DraftContent == "" {
		t.Fatalf("LLM synthesis bypassed review: %+v %v", page, err)
	}
}
