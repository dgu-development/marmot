package quality

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marmotdata/marmot/internal/core/quality"
)

type fakeService struct {
	stored *quality.Stored
	err    error
	seen   int64
}

func (f *fakeService) Settings(context.Context) (*quality.Stored, error) { return f.stored, f.err }

func (f *fakeService) UpdateSettings(_ context.Context, s quality.Settings, expected int64, _ string) (*quality.Stored, error) {
	f.seen = expected
	if f.err != nil {
		return nil, f.err
	}
	return &quality.Stored{Settings: s, Version: expected + 1}, nil
}

func put(h *Handler, header, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
	if header != "" {
		r.Header.Set("If-Match", header)
	}
	w := httptest.NewRecorder()
	h.putSettings(w, r)
	return w
}

func TestIfMatchReadsOneQuotedVersionAndAllowsZero(t *testing.T) {
	for header, want := range map[string]bool{`"0"`: true, `"7"`: true, `0`: false, `"-1"`: false, `"01"`: false, `*`: false, ``: false} {
		if _, ok := parseIfMatch(header); ok != want {
			t.Errorf("%q -> %v", header, ok)
		}
	}
}

func TestReadingSettingsSetsTheETag(t *testing.T) {
	h := &Handler{service: &fakeService{stored: &quality.Stored{Settings: quality.DefaultSettings(), Version: 3}}}
	w := httptest.NewRecorder()
	h.getSettings(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"3"` {
		t.Fatalf("status %d etag %q", w.Code, w.Header().Get("ETag"))
	}
}

func TestSavingNeedsAVersionAndReportsWhatIsWrong(t *testing.T) {
	svc := &fakeService{}
	h := &Handler{service: svc}
	body, _ := json.Marshal(quality.DefaultSettings())

	if w := put(h, "", string(body)); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("no If-Match -> %d", w.Code)
	}
	if w := put(h, "bogus", string(body)); w.Code != http.StatusBadRequest {
		t.Fatalf("bad If-Match -> %d", w.Code)
	}
	if w := put(h, `"0"`, `{"unknown": true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field -> %d", w.Code)
	}
	w := put(h, `"4"`, string(body))
	if w.Code != http.StatusOK || w.Header().Get("ETag") != `"5"` || svc.seen != 4 {
		t.Fatalf("status %d etag %q seen %d", w.Code, w.Header().Get("ETag"), svc.seen)
	}

	svc.err = quality.ErrVersionConflict
	if w := put(h, `"4"`, string(body)); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("conflict -> %d", w.Code)
	}
	svc.err = &quality.ValidationError{Fields: []quality.FieldError{{Field: "weights", Code: "total"}}}
	w = put(h, `"4"`, string(body))
	var got quality.ValidationError
	if e := json.Unmarshal(w.Body.Bytes(), &got); e != nil || w.Code != http.StatusBadRequest || len(got.Fields) != 1 {
		t.Fatalf("validation -> %d %s", w.Code, w.Body)
	}
}
