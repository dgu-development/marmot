package knowledge

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/core/auth"
	"github.com/marmotdata/marmot/internal/core/knowledge"
	"github.com/marmotdata/marmot/internal/store/postgres/pgtest"
)

func TestWikiAccessAndDraftVisibility(t *testing.T) {
	pool := pgtest.TempDB(t)
	id := pgtest.SeedAsset(t, pool)
	svc := knowledge.NewService(pool, knowledge.Options{})
	draft, err := svc.Compile(t.Context(), knowledge.Entity{Kind: "asset", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	reader := auth.NewServiceAccountPrincipal("reader", "reader", nil, []string{"assets:view", "glossary:view"})
	writer := auth.NewServiceAccountPrincipal("writer", "writer", nil, []string{"assets:view", "glossary:view", "knowledge:write"})
	access := Access{}
	as := func(p auth.Principal) context.Context {
		return context.WithValue(t.Context(), common.PrincipalContextKey, p)
	}
	if err := access.Check(as(reader), false); err != nil {
		t.Fatal(err)
	}
	if err := access.Check(as(reader), true); err != ErrForbidden {
		t.Fatalf("reader write access: %v", err)
	}
	if err := access.Check(as(writer), true); err != nil {
		t.Fatal(err)
	}
	restricted := *draft
	PublicPage(&restricted)
	if restricted.DraftContent != "" || restricted.DraftHash != "" || len(restricted.Sources) != 0 {
		t.Fatal("draft leaked to reader")
	}
	if _, err := svc.Publish(t.Context(), draft.Entity, draft.DraftHash, "writer"); err != nil {
		t.Fatal(err)
	}
	published, err := svc.Compile(t.Context(), draft.Entity)
	if err != nil {
		t.Fatal(err)
	}
	restricted = *published
	PublicPage(&restricted)
	if restricted.Content == "" || restricted.DraftHash != "" {
		t.Fatal("published page not visible or draft leaked")
	}
	h := &Handler{service: svc, access: access}
	req := httptest.NewRequest("GET", "/api/v1/knowledge/pages/asset/"+id, nil).WithContext(as(reader))
	req.SetPathValue("entityType", "asset")
	req.SetPathValue("entityId", id)
	out := httptest.NewRecorder()
	h.require(false)(h.get)(out, req)
	if out.Code != 200 {
		t.Fatalf("reader GET: %d %s", out.Code, out.Body.String())
	}
	var got knowledge.Page
	if err := json.Unmarshal(out.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Content == "" || got.DraftHash != "" {
		t.Fatal("GET leaked draft or omitted publication")
	}
	denied := httptest.NewRecorder()
	h.require(true)(h.compile)(denied, req.WithContext(as(reader)))
	if denied.Code != 403 {
		t.Fatalf("reader compile: %d", denied.Code)
	}
}
