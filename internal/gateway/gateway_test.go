package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kuraos-org/kura/i18n"
)

func TestHealthz_GET_ReturnsJSON(t *testing.T) {
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	started := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	h := New(Deps{Translator: tr, Version: "test-1.2.3", StartedAt: started})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	var body healthzResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Version != "test-1.2.3" {
		t.Fatalf("version = %q", body.Version)
	}
	if body.Status == "" || body.Status == string(i18n.MsgSystemHealthy) {
		// status must be the translated text, not the raw MessageID
		t.Fatalf("status = %q (looks untranslated)", body.Status)
	}
	if !body.StartedAt.Equal(started) {
		t.Fatalf("started_at = %v, want %v", body.StartedAt, started)
	}
}

func TestHealthz_RejectsNonGET(t *testing.T) {
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	h := New(Deps{Translator: tr, Version: "x", StartedAt: time.Now()})
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
