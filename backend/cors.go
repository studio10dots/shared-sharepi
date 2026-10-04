package main

import "net/http"

// corsAllowedMethods and corsAllowedHeaders cover every method and header this
// API uses. Authorization carries the bearer ID token (Agents.md section 8),
// so it must always be allowed through a preflight.
const (
	corsAllowedMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	corsAllowedHeaders = "Authorization, Content-Type"
)

// withCORS enforces Agents.md section 15's "Web is single-backend only": a
// browser may call this backend only from its own Web build's origin
// (Config.WebOrigin, set by Terraform's enable_web to that Cloud Run
// service's own URL - empty when Web hosting is not enabled for this
// backend). A request from any other Origin gets no CORS headers at all, so
// the browser blocks the response (a "simple" request) or never sends the
// real request past a failed preflight (anything carrying Authorization,
// which is every authenticated call here) - this is enforced here, not only
// by hiding the option in the app (Agents.md section 15's Web build has no UI
// to add another backend).
//
// A non-browser caller (the mobile app, server-to-server, pentest tooling)
// sends no Origin header and is completely unaffected: CORS is a browser-only
// mechanism.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := s.cfg.WebOrigin != "" && origin == s.cfg.WebOrigin
		if allowed {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", corsAllowedMethods)
			h.Set("Access-Control-Allow-Headers", corsAllowedHeaders)
			h.Set("Access-Control-Max-Age", "3600")
		}
		if r.Method == http.MethodOptions {
			// A preflight never reaches a handler, allowed or not: with no
			// matching Access-Control-Allow-Origin above, the browser already
			// stops here and never sends the real request.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
