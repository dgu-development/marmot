package assets

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/core/asset"
	"github.com/marmotdata/marmot/internal/core/metamodel"
)

func TestParseIfMatch(t *testing.T) {
	if v, ok := parseIfMatch(`"3"`); !ok || v != 3 {
		t.Fatalf("quoted: %d %v", v, ok)
	}
	if v, ok := parseIfMatch("2"); !ok || v != 2 {
		t.Fatalf("bare: %d %v", v, ok)
	}
	for _, header := range []string{"", "*", "0", "-1", "abc"} {
		if _, ok := parseIfMatch(header); ok {
			t.Fatalf("accepted %q", header)
		}
	}
}

func TestRespondAssetWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	if !respondAssetWriteError(rec, &metamodel.ValidationError{Fields: []metamodel.Violation{{Field: "retention", Code: "required"}}}) {
		t.Fatal("validation not mapped")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	var body metamodel.ValidationError
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body.Fields[0].Field != "retention" {
		t.Fatalf("body: %+v %v", body, err)
	}

	rec = httptest.NewRecorder()
	if !respondAssetWriteError(rec, asset.ErrVersionConflict) || rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("conflict status %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	if !respondAssetWriteError(rec, asset.ErrVersionRequired) || rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("required status %d", rec.Code)
	}
	if respondAssetWriteError(httptest.NewRecorder(), asset.ErrAssetNotFound) {
		t.Fatal("unrelated error claimed")
	}
}

func TestMetamodelPatchRouteStaysOffAssetsWildcard(t *testing.T) {
	const want = `"/api/v1/metamodel/assets/{id}"`
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if !strings.Contains(text, want) {
		t.Fatalf("handler missing %s", want)
	}
	for _, banned := range []string{`"/api/v1/assets/{id}/fields"`, "governed/fields"} {
		if strings.Contains(text, banned) {
			t.Fatalf("banned path %s still registered", banned)
		}
	}

	nop := func(http.ResponseWriter, *http.Request) {}
	mux := http.NewServeMux()
	for _, pattern := range []string{
		"/api/v1/assets/{id}/{$}",
		"/api/v1/assets/run-history-histogram/{id}/{$}",
		"/api/v1/assets/tags/{id}/{$}",
		"/api/v1/assets/by-glossary-term/{term_id}/{$}",
		"/api/v1/metamodel/{$}",
		"/api/v1/metamodel/assets/{id}/{$}",
	} {
		mux.HandleFunc(pattern, nop)
	}

	conflict := http.NewServeMux()
	conflict.HandleFunc("/api/v1/assets/run-history-histogram/{id}/{$}", nop)
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		conflict.HandleFunc("/api/v1/assets/{id}/fields/{$}", nop)
	}()
	if !panicked {
		t.Fatal("expected ServeMux conflict for /assets/{id}/fields")
	}
}
