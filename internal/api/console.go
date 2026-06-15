package api

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/kite-plus/kite-kvm/internal/console"
	"github.com/kite-plus/kite-kvm/internal/vm"
)

// vncDialTimeout bounds how long the proxy waits to reach the VM's VNC port.
const vncDialTimeout = 5 * time.Second

type consoleHandler struct {
	service *vm.Service
	tokens  *console.TokenStore
}

// request mints a single-use console token for a running VM and returns the
// websocket path to connect a browser noVNC client to.
func (h *consoleHandler) request(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	host, port, err := h.service.ConsoleEndpoint(r.Context(), id)
	if err != nil {
		writeError(w, mapVMError(err))
		return
	}
	token, ticket, err := h.tokens.Mint(id, host, port)
	if err != nil {
		writeError(w, errInternal("could not mint console token"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":          token,
		"websocket_path": "/console/ws/" + token,
		"expires_at":     ticket.ExpiresAt.UTC(),
	})
}

// serveWS upgrades the request to a websocket and proxies the VNC (RFB) stream
// between the browser and the VM's VNC port. It is authenticated solely by the
// single-use token in the path (browsers cannot send a bearer header), so this
// route is mounted outside the bearer/allowlist middleware.
func (h *consoleHandler) serveWS(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	ticket, ok := h.tokens.Redeem(token)
	if !ok {
		writeError(w, errForbidden("invalid or expired console token"))
		return
	}

	vnc, err := net.DialTimeout("tcp", net.JoinHostPort(ticket.Host, strconv.Itoa(ticket.Port)), vncDialTimeout)
	if err != nil {
		writeError(w, errInternal("could not reach VM console"))
		return
	}
	defer vnc.Close()

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{"binary"},
		// The single-use token is the security boundary; the browser noVNC
		// client typically loads from a different (panel) origin.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer c.CloseNow()

	ws := websocket.NetConn(r.Context(), c, websocket.MessageBinary)
	proxy(ws, vnc)
	_ = c.Close(websocket.StatusNormalClosure, "")
}

// proxy copies bytes bidirectionally between two connections until either side
// closes or errors.
func proxy(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
}
