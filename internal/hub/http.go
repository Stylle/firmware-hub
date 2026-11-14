package hub

import (
	"io/fs"
	"net/http"
	"strings"
)

func (a *App) Handler(web fs.FS) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/backup", a.systemBackup)
	m.HandleFunc("POST /api/v1/restore", a.systemBackup)
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if a.DB.Ping() != nil {
			w.WriteHeader(503)
			return
		}
		jsonOut(w, 200, map[string]string{"status": "ok", "version": Version})
	})
	for _, method := range []string{"GET", "POST", "DELETE"} {
		m.HandleFunc(method+" /api/v1/session", a.auth)
	}
	m.HandleFunc("GET /api/v1/projects", a.projects)
	m.HandleFunc("POST /api/v1/projects", a.projects)
	m.HandleFunc("PUT /api/v1/projects/{id}", a.updateProject)
	m.HandleFunc("GET /api/v1/users", a.users)
	m.HandleFunc("GET /api/v1/activity", a.activity)
	m.HandleFunc("DELETE /api/v1/sessions/{id}", a.revokeSession)
	m.HandleFunc("GET /api/v1/projects/{id}/audit", a.projectAudit)
	m.HandleFunc("GET /api/v1/mfa", a.mfa)
	m.HandleFunc("POST /api/v1/mfa", a.mfa)
	m.HandleFunc("POST /api/v1/password", a.changePassword)
	m.HandleFunc("POST /api/v1/projects/{id}/notifications/test", a.testNotification)
	m.HandleFunc("POST /api/v1/users", a.users)
	m.HandleFunc("GET /api/v1/settings", a.settings)
	m.HandleFunc("PUT /api/v1/settings", a.settings)
	for _, method := range []string{"GET", "POST"} {
		m.HandleFunc(method+" /api/v1/projects/{id}/releases", a.releases)
		m.HandleFunc(method+" /api/v1/projects/{id}/devices", a.devices)
		m.HandleFunc(method+" /api/v1/projects/{id}/members", a.members)
	}
	for _, method := range []string{"GET", "PUT"} {
		m.HandleFunc(method+" /api/v1/projects/{id}/notifications", a.notifications)
	}
	m.HandleFunc("GET /api/v1/projects/{id}/logs", a.logs)
	m.HandleFunc("DELETE /api/v1/projects/{id}/telemetry", a.clearTelemetry)
	m.HandleFunc("DELETE /api/v1/devices/{id}", a.revokeDevice)
	m.HandleFunc("PUT /api/v1/releases/{id}/publish", a.publish)
	m.HandleFunc("GET /api/v1/artifacts/{id}", a.download)
	m.HandleFunc("GET /api/v1/public/{slug}", a.public)
	m.HandleFunc("GET /api/v1/public", a.public)
	m.HandleFunc("GET /api/v1/update/{slug}/check", a.update)
	m.HandleFunc("GET /api/v1/update/{slug}/latest", a.update)
	m.HandleFunc("GET /api/v1/update/{slug}/releases", a.update)
	m.HandleFunc("POST /api/v1/update/{slug}/heartbeat", a.heartbeat)
	if web != nil {
		m.Handle("/", http.FileServer(http.FS(web)))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if a.restoring.Load() && r.URL.Path != "/healthz" {
			fail(w, 503, "restoring", "系统正在恢复，请稍后重新登录")
			return
		}
		m.ServeHTTP(w, r)
	})
}
