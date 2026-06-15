package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kite-plus/kite-kvm/internal/store"
)

func newIdemStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestIdempotencyRequiresKey(t *testing.T) {
	st := newIdemStore(t)
	h := idempotency(st)(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/v1/vms", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing key = %d, want 400", rec.Code)
	}
}

func TestIdempotencyReplaysResponse(t *testing.T) {
	st := newIdemStore(t)
	var calls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"job_id":"abc"}`))
	})
	h := idempotency(st)(handler)

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/vms", strings.NewReader(`{"flavor":"s1"}`))
		req.Header.Set("Idempotency-Key", "key-1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	first := send()
	if first.Code != http.StatusAccepted || first.Body.String() != `{"job_id":"abc"}` {
		t.Fatalf("first response = %d %q", first.Code, first.Body.String())
	}

	second := send()
	if second.Code != http.StatusAccepted || second.Body.String() != `{"job_id":"abc"}` {
		t.Fatalf("replay response = %d %q", second.Code, second.Body.String())
	}
	if second.Header().Get("Idempotent-Replayed") != "true" {
		t.Error("replay should set Idempotent-Replayed header")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("handler invoked %d times, want 1 (retry must not re-run)", got)
	}
}

func TestIdempotencyKeyReuseDifferentBody(t *testing.T) {
	st := newIdemStore(t)
	h := idempotency(st)(okHandler())

	req1 := httptest.NewRequest(http.MethodPost, "/v1/vms", strings.NewReader(`{"a":1}`))
	req1.Header.Set("Idempotency-Key", "key-2")
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first = %d", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/v1/vms", strings.NewReader(`{"a":2}`))
	req2.Header.Set("Idempotency-Key", "key-2")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Errorf("reuse with different body = %d, want 409", rec2.Code)
	}
}
