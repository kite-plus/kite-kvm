package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/kite-plus/kite-kvm/internal/console"
)

// echoServer starts a TCP server that echoes everything back, standing in for a
// VM's VNC port. It returns its host and port.
func echoServer(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func TestConsoleProxy(t *testing.T) {
	host, port := echoServer(t)

	tokens := console.NewTokenStore(time.Minute)
	tok, _, err := tokens.Mint("vm1", host, port)
	if err != nil {
		t.Fatal(err)
	}

	h := &consoleHandler{tokens: tokens}
	r := chi.NewRouter()
	r.Get("/console/ws/{token}", h.serveWS)
	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/console/ws/" + tok
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.CloseNow()

	conn := websocket.NetConn(ctx, c, websocket.MessageBinary)
	payload := []byte("RFB 003.008\n")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != string(payload) {
		t.Errorf("echo = %q, want %q", buf, payload)
	}
}

func TestConsoleProxyBadToken(t *testing.T) {
	h := &consoleHandler{tokens: console.NewTokenStore(time.Minute)}
	r := chi.NewRouter()
	r.Get("/console/ws/{token}", h.serveWS)

	req := httptest.NewRequest(http.MethodGet, "/console/ws/bogus", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("bad token = %d, want 403", rec.Code)
	}
}
