package quality

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/marmotdata/marmot/internal/core/quality"
)

type fakeRuns struct {
	run     *quality.Run
	err     error
	filter  quality.ResultFilter
	limit   int
	offset  int
	trigger string
}

func (f *fakeRuns) StartRun(_ context.Context, trigger, _ string) (*quality.Run, error) {
	f.trigger = trigger
	return f.run, f.err
}

func (f *fakeRuns) Runs(_ context.Context, limit, offset int) ([]quality.Run, int, error) {
	f.limit, f.offset = limit, offset
	return []quality.Run{{ID: "r1"}}, 7, f.err
}

func (f *fakeRuns) Run(context.Context, string) (*quality.Run, error) { return f.run, f.err }

func (f *fakeRuns) Results(_ context.Context, _ string, filter quality.ResultFilter) ([]quality.AssetResult, int, error) {
	f.filter = filter
	return []quality.AssetResult{{AssetID: "a"}}, 1, f.err
}

func (f *fakeRuns) Shutdown() {}

func do(handler http.HandlerFunc, method, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.SetPathValue("id", "r1")
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func TestStartingARunAnswersAcceptedWithWhereToLookAtIt(t *testing.T) {
	runs := &fakeRuns{run: &quality.Run{ID: "r1", Status: quality.RunRunning}}
	h := &Handler{runs: runs}
	w := do(h.startRun, http.MethodPost, "/")
	if w.Code != http.StatusAccepted || w.Header().Get("Location") != "/api/v1/quality/runs/r1" || runs.trigger != quality.TriggerManual {
		t.Fatalf("%d %q %q", w.Code, w.Header().Get("Location"), runs.trigger)
	}
}

func TestStartingWhileOneRunsConflictsAndNamesIt(t *testing.T) {
	h := &Handler{runs: &fakeRuns{run: &quality.Run{ID: "r0"}, err: quality.ErrRunInProgress}}
	w := do(h.startRun, http.MethodPost, "/")
	var body RunConflict
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusConflict || body.Run == nil || body.Run.ID != "r0" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestStartingWithoutAMetamodelIsUnprocessableAndAFailureIsAnInternalError(t *testing.T) {
	if w := do((&Handler{runs: &fakeRuns{err: quality.ErrMetamodelOff}}).startRun, http.MethodPost, "/"); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no metamodel -> %d", w.Code)
	}
	w := do((&Handler{runs: &fakeRuns{err: errors.New("pq: password=secret")}}).startRun, http.MethodPost, "/")
	if w.Code != http.StatusInternalServerError || json.Valid(w.Body.Bytes()) && len(w.Body.String()) > 60 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestRunListsAreBoundedAndDefaulted(t *testing.T) {
	runs := &fakeRuns{}
	h := &Handler{runs: runs}
	for target, want := range map[string][2]int{"/": {20, 0}, "/?limit=5&offset=10": {5, 10}, "/?limit=100000": {100, 0}, "/?limit=-3&offset=x": {20, 0}, "/?limit=0": {20, 0}} {
		w := do(h.listRuns, http.MethodGet, target)
		if w.Code != http.StatusOK || runs.limit != want[0] || runs.offset != want[1] {
			t.Errorf("%s -> %d limit %d offset %d", target, w.Code, runs.limit, runs.offset)
		}
	}
	var body RunsResponse
	_ = json.Unmarshal(do(h.listRuns, http.MethodGet, "/").Body.Bytes(), &body)
	if body.Total != 7 || len(body.Runs) != 1 || body.Limit != 20 {
		t.Fatalf("%+v", body)
	}
}

func TestAnUnknownRunIsNotFound(t *testing.T) {
	h := &Handler{runs: &fakeRuns{err: quality.ErrRunNotFound}}
	if w := do(h.getRun, http.MethodGet, "/"); w.Code != http.StatusNotFound {
		t.Fatalf("run -> %d", w.Code)
	}
	if w := do(h.getResults, http.MethodGet, "/"); w.Code != http.StatusNotFound {
		t.Fatalf("results -> %d", w.Code)
	}
}

func TestResultFiltersAreReadAndValidated(t *testing.T) {
	runs := &fakeRuns{}
	h := &Handler{runs: runs}
	w := do(h.getResults, http.MethodGet, "/?status=warning&domain=unassigned&type=table&q=cli&sort=name&limit=9&offset=18")
	want := quality.ResultFilter{Status: quality.StatusWarning, Domain: "unassigned", Type: "table", Query: "cli", Sort: "name", Limit: 9, Offset: 18}
	if w.Code != http.StatusOK || runs.filter != want {
		t.Fatalf("%d %+v", w.Code, runs.filter)
	}
	for _, target := range []string{"/?status=bogus", "/?sort=score"} {
		if w := do(h.getResults, http.MethodGet, target); w.Code != http.StatusBadRequest {
			t.Errorf("%s -> %d", target, w.Code)
		}
	}
	var body ResultsResponse
	_ = json.Unmarshal(do(h.getResults, http.MethodGet, "/").Body.Bytes(), &body)
	if body.Limit != quality.DefaultResultLimit || body.Total != 1 {
		t.Fatalf("%+v", body)
	}
}

func TestEveryRunRouteIsGuardedByItsPermission(t *testing.T) {
	routes := (&Handler{}).Routes()
	got := map[string]int{}
	for _, route := range routes {
		got[route.Method+" "+route.Path] = len(route.Middleware)
	}
	for _, key := range []string{"POST /api/v1/quality/runs", "GET /api/v1/quality/runs", "GET /api/v1/quality/runs/{id}", "GET /api/v1/quality/runs/{id}/results"} {
		if got[key] != 2 {
			t.Errorf("%s has %d middleware, want authentication and a permission", key, got[key])
		}
	}
}
