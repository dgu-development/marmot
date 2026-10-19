package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponseWriterLetsHandlersFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	wrapped := &responseWriter{ResponseWriter: recorder, statusCode: http.StatusOK}

	if _, err := wrapped.Write([]byte("data: 1\n\n")); err != nil {
		t.Fatal(err)
	}
	if err := http.NewResponseController(wrapped).Flush(); err != nil {
		t.Fatalf("flush through the metrics writer: %v", err)
	}
	if !recorder.Flushed {
		t.Fatal("the underlying writer was not flushed")
	}
}
