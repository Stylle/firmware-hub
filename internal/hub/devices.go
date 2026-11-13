package hub

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Stylle/firmware-hub/internal/protocol"
)

type deviceIdentity struct {
	ID    int64
	Valid bool
}

func (a *App) deviceAuth(r *http.Request, pid int64) deviceIdentity {
	var d deviceIdentity
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return d
	}
	t := strings.TrimPrefix(v, "Bearer ")
	if len(t) != 64 {
		return d
	}
	if a.DB.QueryRow("SELECT id FROM devices WHERE project_id=? AND token_hash=? AND revoked=0", pid, digest(t)).Scan(&d.ID) == nil {
		d.Valid = true
	}
	return d
}
func (a *App) devices(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	u, ok := a.allowed(w, r, id, r.Method != "GET", false)
	if !ok {
		return
	}
	p, e := a.project(id)
	if e != nil {
		fail(w, 404, "not_found", "项目不存在")
		return
	}
	if r.Method == "POST" {
		var in struct {
			Name string `json:"name"`
		}
		if readJSON(w, r, &in) != nil || len(strings.TrimSpace(in.Name)) == 0 || utf8.RuneCountInString(in.Name) > 100 {
			fail(w, 400, "invalid_name", "设备名称为 1–100 字符")
			return
		}
		token := randomToken()
		res, e := a.DB.Exec("INSERT INTO devices(project_id,name,token_hash,created) VALUES(?,?,?,?)", id, in.Name, digest(token), now())
		if e != nil {
			fail(w, 500, "database_error", "创建失败")
			return
		}
		did, _ := res.LastInsertId()
		a.audit(u.ID, p.Slug, "device.create")
		jsonOut(w, 201, map[string]any{"id": did, "token": token, "message": "凭据仅显示一次，请安全保存"})
		return
	}
	var global bool
	_ = a.DB.QueryRow("SELECT telemetry FROM settings WHERE id=1").Scan(&global)
	rows, e := a.DB.Query("SELECT id,name,revoked,version,platform,arch,hardware,status,last_seen,created FROM devices WHERE project_id=? ORDER BY id DESC LIMIT 1000", id)
	if e != nil {
		fail(w, 500, "database_error", "读取设备失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var did, last, created int64
		var name, version, platform, arch, hardware, status string
		var revoked bool
		if rows.Scan(&did, &name, &revoked, &version, &platform, &arch, &hardware, &status, &last, &created) != nil {
			fail(w, 500, "database_error", "读取失败")
			return
		}
		state := "unknown"
		if !global || !p.Telemetry {
			state = "disabled"
		} else if revoked {
			state = "revoked"
		} else if last > 0 {
			state = "offline"
			if now()-last <= int64(p.Timeout) {
				state = "online"
			}
		}
		out = append(out, map[string]any{"id": did, "name": name, "revoked": revoked, "version": version, "platform": platform, "arch": arch, "hardware": hardware, "status": status, "last_seen": last, "created": created, "online_state": state})
	}
	jsonOut(w, 200, out)
}
func (a *App) revokeDevice(w http.ResponseWriter, r *http.Request) {
	did := pathID(r)
	var pid int64
	if a.DB.QueryRow("SELECT project_id FROM devices WHERE id=?", did).Scan(&pid) != nil {
		fail(w, 404, "not_found", "设备不存在")
		return
	}
	u, ok := a.allowed(w, r, pid, true, false)
	if !ok {
		return
	}
	if _, e := a.DB.Exec("UPDATE devices SET revoked=1 WHERE id=?", did); e != nil {
		fail(w, 500, "database_error", "撤销失败")
		return
	}
	a.auditProject(u.ID, pid, "device.revoke")
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *App) heartbeat(w http.ResponseWriter, r *http.Request) {
	var pid int64
	if a.DB.QueryRow("SELECT id FROM projects WHERE slug=?", r.PathValue("slug")).Scan(&pid) != nil {
		fail(w, 404, "not_found", "项目不存在")
		return
	}
	d := a.deviceAuth(r, pid)
	if !d.Valid {
		fail(w, 401, "invalid_device", "设备凭据无效或已撤销")
		return
	}
	p, _ := a.project(pid)
	var global bool
	_ = a.DB.QueryRow("SELECT telemetry FROM settings WHERE id=1").Scan(&global)
	if !global || !p.Telemetry {
		fail(w, 403, "telemetry_disabled", "Hub 或项目已关闭设备状态采集")
		return
	}
	var in struct {
		Version  string `json:"version"`
		Platform string `json:"platform"`
		Arch     string `json:"arch"`
		Hardware string `json:"hardware"`
		Status   string `json:"status"`
	}
	if readJSON(w, r, &in) != nil {
		fail(w, 400, "invalid_report", "状态格式无效")
		return
	}
	for _, s := range []string{in.Version, in.Platform, in.Arch, in.Hardware} {
		if !protocol.Identifier.MatchString(s) {
			fail(w, 400, "invalid_report", "版本与平台字段必须是有效标识")
			return
		}
	}
	switch in.Status {
	case "idle", "downloading", "verified", "installing", "confirmed", "failed":
	default:
		fail(w, 400, "invalid_status", "升级状态无效")
		return
	}
	var last int64
	_ = a.DB.QueryRow("SELECT last_seen FROM devices WHERE id=?", d.ID).Scan(&last)
	if now()-last < 5 {
		fail(w, 429, "heartbeat_too_frequent", "心跳间隔至少 5 秒")
		return
	}
	if _, e := a.DB.Exec("UPDATE devices SET version=?,platform=?,arch=?,hardware=?,status=?,last_seen=? WHERE id=?", in.Version, in.Platform, in.Arch, in.Hardware, in.Status, now(), d.ID); e != nil {
		fail(w, 500, "database_error", "保存状态失败")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "interval_seconds": p.Heartbeat})
}
func (a *App) clearTelemetry(w http.ResponseWriter, r *http.Request) {
	pid := pathID(r)
	u, ok := a.allowed(w, r, pid, true, false)
	if !ok {
		return
	}
	_, e := a.DB.Exec("UPDATE devices SET version='',platform='',arch='',hardware='',status='unknown',last_seen=0 WHERE project_id=?", pid)
	if e != nil {
		fail(w, 500, "database_error", "清除失败")
		return
	}
	a.auditProject(u.ID, pid, "telemetry.clear")
	jsonOut(w, 200, map[string]bool{"ok": true})
}
