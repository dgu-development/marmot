package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/api/v1/common"
	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/auth"
	"github.com/marmotdata/marmot/internal/core/domain"
	"github.com/marmotdata/marmot/internal/core/metamodel"
	"github.com/marmotdata/marmot/internal/core/user"
	"github.com/marmotdata/marmot/internal/core/workflow"
)

func request(method, body string, p auth.Principal) *http.Request {
	r := httptest.NewRequest(method, "/", strings.NewReader(body))
	if p != nil {
		r = r.WithContext(context.WithValue(r.Context(), common.PrincipalContextKey, p))
	}
	return r
}

func TestValidateReturnsTheIssuesOfADiagram(t *testing.T) {
	h := &Handler{}
	body, _ := json.Marshal(DefinitionRequest{BPMN: `<bpmn:definitions xmlns:bpmn="` + workflow.NamespaceBPMN + `"><bpmn:process id="p"><bpmn:scriptTask id="x"/></bpmn:process></bpmn:definitions>`})
	w := httptest.NewRecorder()

	h.validate(w, request(http.MethodPost, string(body), nil))

	var got ValidateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
	if got.Valid || len(got.Issues) == 0 || got.Issues[0].Code != "script_not_allowed" {
		t.Fatalf("response = %+v", got)
	}
}

func TestCreatingADefinitionNeedsTheManagePermission(t *testing.T) {
	h := &Handler{service: workflow.NewService(nil, nil, nil, nil, nil, nil)}
	viewer := auth.NewUserPrincipal(&user.User{ID: "u", Username: "viewer", Roles: []user.Role{{Name: "user", Permissions: []user.Permission{{ResourceType: "workflows", Action: "view"}}}}})
	body, _ := json.Marshal(DefinitionRequest{BPMN: "<x/>"})
	w := httptest.NewRecorder()

	h.createDefinition(w, request(http.MethodPost, string(body), viewer))

	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
}

func TestErrorsMapToStableCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&workflow.ValidationError{Issues: []workflow.Issue{{Code: "no_start"}}}, http.StatusBadRequest, "invalid_diagram"},
		{fmt.Errorf("%w: bad", workflow.ErrInvalidInput), http.StatusBadRequest, "invalid_input"},
		{workflow.ErrForbidden, http.StatusForbidden, "forbidden"},
		{workflow.ErrNotFound, http.StatusNotFound, "not_found"},
		{workflow.ErrNotRunnable, http.StatusConflict, "not_published"},
		{workflow.ErrNotRunning, http.StatusConflict, "not_running"},
		{workflow.ErrConflict, http.StatusConflict, "conflict"},
		{errors.New("boom"), http.StatusInternalServerError, "internal"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		respondErr(w, tc.err)
		var got ErrorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if w.Code != tc.status || got.Code != tc.code {
			t.Errorf("%v: %d %s, want %d %s", tc.err, w.Code, got.Code, tc.status, tc.code)
		}
		if tc.code == "invalid_diagram" && len(got.Issues) != 1 {
			t.Errorf("issues missing: %s", w.Body)
		}
		if tc.code == "internal" && strings.Contains(w.Body.String(), "boom") {
			t.Errorf("internal error leaked: %s", w.Body)
		}
	}
}

func TestRoutesDoNotCollide(t *testing.T) {
	mux := http.NewServeMux()
	for _, r := range (&Handler{}).Routes() {
		mux.HandleFunc(r.Method+" "+r.Path, func(http.ResponseWriter, *http.Request) {})
	}
}

func TestRefusedFieldValuesAreABadRequestWithTheirFields(t *testing.T) {
	err := fmt.Errorf("writing the form: %w", &metamodel.ValidationError{Fields: []metamodel.Violation{{Field: "classification", Code: "enum"}}})
	w := httptest.NewRecorder()

	respondErr(w, err)

	var got ErrorResponse
	if e := json.Unmarshal(w.Body.Bytes(), &got); e != nil {
		t.Fatal(e)
	}
	if w.Code != http.StatusBadRequest || got.Code != "invalid_fields" || len(got.Fields) != 1 || got.Fields[0].Field != "classification" {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
}

func TestAssetErrorsKeepTheirMeaning(t *testing.T) {
	cases := map[error]int{
		asset.ErrVersionConflict: http.StatusConflict,
		domain.ErrForbidden:      http.StatusForbidden,
	}
	for err, want := range cases {
		w := httptest.NewRecorder()
		respondErr(w, fmt.Errorf("wrapped: %w", err))
		if w.Code != want {
			t.Errorf("%v -> %d, want %d", err, w.Code, want)
		}
	}
}
