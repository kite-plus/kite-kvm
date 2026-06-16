package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kite-plus/kite-kvm/internal/model"
	"github.com/kite-plus/kite-kvm/internal/store"
)

func newStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestIdempotencyStaleReclaim(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	ran := 0
	h := idempotency(st)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	hash := hashRequest(http.MethodPost, "/v1/vms", []byte("{}"))

	send := func(key string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/vms", strings.NewReader("{}"))
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	// A recent in-progress claim is held by a concurrent request -> 409.
	_ = st.PutIdempotency(ctx, &model.IdempotencyRecord{
		Key: "recent", RequestHash: hash, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	if code := send("recent"); code != http.StatusConflict {
		t.Errorf("recent in-progress = %d, want 409", code)
	}
	if ran != 0 {
		t.Errorf("handler must not run for an in-progress claim, ran=%d", ran)
	}

	// A stale (crashed) in-progress claim is reclaimed -> handler runs.
	_ = st.PutIdempotency(ctx, &model.IdempotencyRecord{
		Key: "stale", RequestHash: hash, CreatedAt: time.Now().Add(-20 * time.Minute), ExpiresAt: time.Now().Add(time.Hour),
	})
	if code := send("stale"); code != http.StatusAccepted {
		t.Errorf("stale-claim reclaim = %d, want 202", code)
	}
	if ran != 1 {
		t.Errorf("handler must run after reclaim, ran=%d", ran)
	}
}
