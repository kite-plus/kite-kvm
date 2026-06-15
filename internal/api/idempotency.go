package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/kite-plus/kite-kvm/internal/model"
	"github.com/kite-plus/kite-kvm/internal/store"
)

// idempotencyTTL is how long a stored response may be replayed.
const idempotencyTTL = 24 * time.Hour

// maxIdempotentBody bounds how much request body is read for hashing.
const maxIdempotentBody = 1 << 20 // 1 MiB

// idempotency enforces the Idempotency-Key header on mutating requests. The
// first request with a key runs the handler and stores its response; retries
// with the same key replay the stored response (a retried create never
// provisions twice). A key reused with a different request body is rejected.
func idempotency(st store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				writeError(w, errBadRequest("Idempotency-Key header is required"))
				return
			}

			body, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotentBody))
			if err != nil {
				writeError(w, errBadRequest("could not read request body"))
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			hash := hashRequest(r.Method, r.URL.Path, body)

			existing, err := st.GetIdempotency(r.Context(), key)
			switch {
			case err == nil:
				if existing.RequestHash != hash {
					writeError(w, errConflict("Idempotency-Key already used with a different request"))
					return
				}
				if len(existing.Response) > 0 {
					replay(w, existing)
					return
				}
				writeError(w, errConflict("a request with this Idempotency-Key is already in progress"))
				return
			case !errors.Is(err, store.ErrNotFound):
				writeError(w, err)
				return
			}

			// Claim the key as a lock before running the handler.
			rec := &model.IdempotencyRecord{
				Key:         key,
				RequestHash: hash,
				ExpiresAt:   time.Now().Add(idempotencyTTL),
			}
			if err := st.PutIdempotency(r.Context(), rec); err != nil {
				if errors.Is(err, store.ErrConflict) {
					writeError(w, errConflict("a request with this Idempotency-Key is already in progress"))
					return
				}
				writeError(w, err)
				return
			}

			cap := &responseCapture{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(cap, r)

			rec.Response = cap.body.Bytes()
			rec.StatusCode = cap.status
			if err := st.UpdateIdempotency(r.Context(), rec); err != nil {
				// The client already has the response; just log-and-ignore is not
				// available here, so swallow the persistence error.
				_ = err
			}
		})
	}
}

func replay(w http.ResponseWriter, rec *model.IdempotencyRecord) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Idempotent-Replayed", "true")
	w.WriteHeader(rec.StatusCode)
	_, _ = w.Write(rec.Response)
}

func hashRequest(method, path string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{'\n'})
	h.Write([]byte(path))
	h.Write([]byte{'\n'})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// responseCapture tees the handler's response into a buffer while writing it
// through to the client, so it can be stored for replay.
type responseCapture struct {
	http.ResponseWriter
	status      int
	body        bytes.Buffer
	wroteHeader bool
}

func (c *responseCapture) WriteHeader(code int) {
	if c.wroteHeader {
		return
	}
	c.status = code
	c.wroteHeader = true
	c.ResponseWriter.WriteHeader(code)
}

func (c *responseCapture) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}
