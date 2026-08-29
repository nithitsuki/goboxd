// Auth middleware gates POST /run.
//
// goboxd is a server-to-server backend: the integration (Code Royale) calls
// it over HTTPS with a shared bearer token and sends no Origin header.
// A browser-originated request always carries an Origin header, so the Origin
// check is the browser filter.
//
// Environment:
//   - GOBOXD_AUTH_TOKEN: required bearer token. Empty disables the gate
//     (local/dev runs); the production deployment always sets it.
//   - GOBOXD_ALLOWED_ORIGINS: comma-separated origin allowlist. Empty means
//     every browser origin is rejected, which is what the production
//     deployment wants (unset on purpose).
package api

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
)

func authConfig() (token string, origins []string) {
	token = strings.TrimSpace(os.Getenv("GOBOXD_AUTH_TOKEN"))
	for _, part := range strings.Split(os.Getenv("GOBOXD_ALLOWED_ORIGINS"), ",") {
		if p := strings.TrimSpace(part); p != "" {
			origins = append(origins, p)
		}
	}
	return token, origins
}

// validBearer reports whether the Authorization header carries a constant-time
// match for the configured token. An unconfigured token (empty) accepts any
// request so local/dev runs keep working.
func validBearer(got, want string) bool {
	if want == "" {
		return true
	}
	if !strings.HasPrefix(got, "Bearer ") {
		return false
	}
	have := strings.TrimPrefix(got, "Bearer ")
	return subtle.ConstantTimeCompare([]byte(have), []byte(want)) == 1
}

// originAllowed reports whether a request's Origin passes the allowlist.
// A missing Origin is the server-to-server case and is always allowed. With
// no configured origins, any browser origin is rejected.
func originAllowed(origin string, allowed []string) bool {
	if origin == "" {
		return true
	}
	if len(allowed) == 0 {
		return false
	}
	for _, o := range allowed {
		if o == origin {
			return true
		}
	}
	return false
}

// AuthMiddleware enforces the bearer token and Origin allowlist on POST /run.
// Read-only endpoints (healthz, readyz, info, ...) stay open so they remain
// probeable by load balancers and the tunnel.
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/run" {
			token, origins := authConfig()
			if !validBearer(r.Header.Get("Authorization"), token) {
				writeErrorStatus(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
				return
			}
			if !originAllowed(r.Header.Get("Origin"), origins) {
				writeErrorStatus(w, http.StatusForbidden, "forbidden_origin", "origin not allowed")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}