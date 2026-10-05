package glossary

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/marmotdata/marmot/internal/core/glossary"
)

type updateSpy struct {
	core.Service
	id    string
	input core.UpdateTermInput
}

func (s *updateSpy) Update(_ context.Context, id string, input core.UpdateTermInput) (*core.GlossaryTerm, error) {
	s.id = id
	s.input = input
	return &core.GlossaryTerm{ID: id, Name: "Revenue", Tags: input.Tags}, nil
}

func TestUpdateTerm_PassesTags(t *testing.T) {
	spy := &updateSpy{}
	h := &Handler{glossaryService: spy}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/glossary/term-1", strings.NewReader(`{"tags":["pii","finance"]}`))
	rec := httptest.NewRecorder()

	h.updateTerm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if spy.id != "term-1" {
		t.Fatalf("id = %q", spy.id)
	}
	if len(spy.input.Tags) != 2 || spy.input.Tags[0] != "pii" || spy.input.Tags[1] != "finance" {
		t.Fatalf("tags = %#v", spy.input.Tags)
	}
}

func TestUpdateTerm_ClearsTags(t *testing.T) {
	spy := &updateSpy{}
	h := &Handler{glossaryService: spy}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/glossary/term-1", strings.NewReader(`{"tags":[]}`))
	rec := httptest.NewRecorder()

	h.updateTerm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if spy.input.Tags == nil || len(spy.input.Tags) != 0 {
		t.Fatalf("tags = %#v, want an empty list", spy.input.Tags)
	}
}

func TestUpdateTerm_OmitsTagsWhenAbsent(t *testing.T) {
	spy := &updateSpy{}
	h := &Handler{glossaryService: spy}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/glossary/term-1", strings.NewReader(`{"name":"Revenue"}`))
	rec := httptest.NewRecorder()

	h.updateTerm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if spy.input.Tags != nil {
		t.Fatalf("tags = %#v, want nil so an unrelated update keeps them", spy.input.Tags)
	}
	if spy.input.Name == nil || *spy.input.Name != "Revenue" {
		t.Fatalf("name = %#v", spy.input.Name)
	}
}
