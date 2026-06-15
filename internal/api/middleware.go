package api

import (
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// bearerAuth rejects requests without a valid `Authorization: Bearer <token>`
// header matching one of the configured tokens. Comparison is constant-time.
func bearerAuth(tokens []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := bearerToken(r)
			if !ok || !tokenAllowed(tokens, tok) {
				writeError(w, errUnauthorized("missing or invalid bearer token"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

// tokenAllowed reports whether provided matches any configured token. It checks
// every token (no early return) to avoid leaking which token matched via timing.
func tokenAllowed(tokens []string, provided string) bool {
	match := 0
	for _, t := range tokens {
		match |= subtle.ConstantTimeCompare([]byte(t), []byte(provided))
	}
	return match == 1
}

// ipAllowlist rejects requests whose direct peer address is not covered by the
// allowlist. An empty allowlist permits all sources. It deliberately uses the
// connection's RemoteAddr (never X-Forwarded-For), which cannot be spoofed.
func ipAllowlist(entries []string, logger *slog.Logger) func(http.Handler) http.Handler {
	nets, ips := parseAllowlist(entries)
	allowAll := len(entries) == 0
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allowAll {
				next.ServeHTTP(w, r)
				return
			}
			ip := clientIP(r)
			if ip == nil || !ipMatches(ip, nets, ips) {
				if logger != nil {
					logger.Warn("rejected request from disallowed source", "remote", r.RemoteAddr, "path", r.URL.Path)
				}
				writeError(w, errForbidden("source address not allowed"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func parseAllowlist(entries []string) ([]*net.IPNet, []net.IP) {
	var nets []*net.IPNet
	var ips []net.IP
	for _, e := range entries {
		if strings.Contains(e, "/") {
			if _, n, err := net.ParseCIDR(e); err == nil {
				nets = append(nets, n)
			}
			continue
		}
		if ip := net.ParseIP(e); ip != nil {
			ips = append(ips, ip)
		}
	}
	return nets, ips
}

func ipMatches(ip net.IP, nets []*net.IPNet, ips []net.IP) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	for _, a := range ips {
		if a.Equal(ip) {
			return true
		}
	}
	return false
}

func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}
