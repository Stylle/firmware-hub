package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Stylle/firmware-hub/internal/protocol"
)

type release struct {
	ID        int64  `json:"id"`
	Version   string `json:"version"`
	Sequence  int64  `json:"sequence"`
	Security  int64  `json:"security"`
	Channel   string `json:"channel"`
	Platform  string `json:"platform"`
	Arch      string `json:"arch"`
	Hardware  string `json:"hardware"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
	Notes     string `json:"notes"`
	Published bool   `json:"published"`
	Created   int64  `json:"created"`
	Download  string `json:"download"`
}

const releaseCols = "id,version,sequence,security,channel,platform,arch,hardware,sha256,size,payload,signature,notes,published,created"

type scanner interface{ Scan(...any) error }

func scanRelease(s scanner) (release, error) {
	var v release
	e := s.Scan(&v.ID, &v.Version, &v.Sequence, &v.Security, &v.Channel, &v.Platform, &v.Arch, &v.Hardware, &v.SHA256, &v.Size, &v.Payload, &v.Signature, &v.Notes, &v.Published, &v.Created)
	v.Download = fmt.Sprintf("/api/v1/artifacts/%d", v.ID)
	return v, e
}
func (a *App) listReleases(pid int64, published bool) ([]release, error) {
	return a.releasePage(pid, published, 0, 200)
}
func (a *App) releasePage(pid int64, published bool, before int64, limit int) ([]release, error) {
	q := "SELECT " + releaseCols + " FROM releases WHERE project_id=?"
	args := []any{pid}
	if published {
		q += " AND published=1"
	}
	if before > 0 {
		q += " AND sequence<?"
		args = append(args, before)
	}
	q += " ORDER BY sequence DESC LIMIT ?"
	args = append(args, limit)
	rows, e := a.DB.Query(q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []release{}
	for rows.Next() {
		v, e := scanRelease(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (a *App) pagedReleases(w http.ResponseWriter, r *http.Request, pid int64, published bool) ([]release, bool) {
	q := r.URL.Query()
	limit := 200
	before := int64(0)
	var e error
	if q.Get("limit") != "" {
		limit, e = strconv.Atoi(q.Get("limit"))
		if e != nil || limit < 1 || limit > 200 {
			fail(w, 400, "invalid_page", "limit 必须是 1–200")
			return nil, false
		}
	}
	if q.Get("before") != "" {
		before, e = strconv.ParseInt(q.Get("before"), 10, 64)
		if e != nil || before < 1 {
			fail(w, 400, "invalid_page", "before 必须是正整数发布序号")
			return nil, false
		}
	}
	out, e := a.releasePage(pid, published, before, limit)
	if e != nil {
		fail(w, 500, "database_error", "读取版本失败")
		return nil, false
	}
	if len(out) == limit {
		w.Header().Set("X-Next-Before", strconv.FormatInt(out[len(out)-1].Sequence, 10))
	}
	return out, true
}
func (a *App) compatible(pid int64, key []byte, q url.Values, current, security int64, channel string) (*release, error) {
	rows, e := a.DB.Query("SELECT "+releaseCols+" FROM releases WHERE project_id=? AND published=1 AND channel=? AND security>=? AND platform IN ('any',?) AND arch IN ('any',?) AND hardware IN ('any',?) ORDER BY sequence DESC", pid, channel, security, q.Get("platform"), q.Get("arch"), q.Get("hardware"))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scanRelease(rows)
		if e != nil {
			return nil, e
		}
		m, e := (protocol.Envelope{Payload: v.Payload, Signature: v.Signature}).Verify(key)
		if e == nil && m.Expires > now() && m.MinSequence <= current {
			return &v, nil
		}
	}
	return nil, rows.Err()
}
func (a *App) releases(w http.ResponseWriter, r *http.Request) {
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
	if r.Method == "GET" {
		out, ok := a.pagedReleases(w, r, id, false)
		if !ok {
			return
		}
		jsonOut(w, 200, out)
		return
	}
	select {
	case a.uploadSlots <- struct{}{}:
		defer func() { <-a.uploadSlots }()
	default:
		fail(w, 429, "upload_busy", "上传并发已满，请稍后重试")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (1<<30)+(64<<10))
	mr, e := r.MultipartReader()
	if e != nil {
		fail(w, 400, "invalid_upload", "请使用 multipart 上传，顺序为 manifest、notes、file")
		return
	}
	part, e := mr.NextPart()
	if e != nil || part.FormName() != "manifest" {
		fail(w, 400, "manifest_required", "首先提供签名 manifest")
		return
	}
	b, e := io.ReadAll(io.LimitReader(part, 8193))
	if e != nil || len(b) > 8192 {
		fail(w, 400, "invalid_manifest", "清单过大")
		return
	}
	var env protocol.Envelope
	if json.Unmarshal(b, &env) != nil {
		fail(w, 400, "invalid_manifest", "签名清单 JSON 无效")
		return
	}
	key, _ := hex.DecodeString(p.PublicKey)
	m, e := env.Verify(key)
	if e != nil || m.Project != p.Slug || m.Expires <= now() || m.Size > 1<<30 {
		fail(w, 400, "invalid_signature", "签名、项目、有效期或文件大小不符合要求")
		return
	}
	part, e = mr.NextPart()
	notes := ""
	if e == nil && part.FormName() == "notes" {
		b, e = io.ReadAll(io.LimitReader(part, 32769))
		if e != nil || len(b) > 32768 {
			fail(w, 400, "invalid_notes", "版本说明最大 32 KiB")
			return
		}
		notes = string(b)
		part, e = mr.NextPart()
	}
	if e != nil || part.FormName() != "file" {
		fail(w, 400, "file_required", "缺少固件文件")
		return
	}
	f, e := os.CreateTemp(filepath.Join(a.Data, "artifacts"), ".upload-")
	if e != nil {
		fail(w, 507, "storage_error", "无法写入固件存储")
		return
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	_ = f.Chmod(0600)
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(part, m.Size+1))
	if e != nil || n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		fail(w, 400, "integrity_error", "文件大小或 SHA-256 与签名清单不一致")
		return
	}
	if _, e = mr.NextPart(); e != io.EOF {
		fail(w, 400, "unexpected_part", "上传包含额外字段")
		return
	}
	if f.Sync() != nil || f.Close() != nil {
		fail(w, 507, "storage_error", "保存文件失败")
		return
	}
	target := filepath.Join(a.Data, "artifacts", m.SHA256)
	if _, e = os.Stat(target); os.IsNotExist(e) {
		if e = os.Rename(temp, target); e != nil {
			fail(w, 507, "storage_error", "固件落盘失败")
			return
		}
	}
	res, e := a.DB.Exec("INSERT INTO releases(project_id,version,sequence,security,channel,platform,arch,hardware,sha256,size,payload,signature,notes,created) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", id, m.Version, m.Sequence, m.Security, m.Channel, m.Platform, m.Arch, m.Hardware, m.SHA256, m.Size, env.Payload, env.Signature, notes, now())
	if e != nil {
		fail(w, 409, "release_exists", "版本或发布序号已存在，已发布文件不能覆盖")
		return
	}
	rid, _ := res.LastInsertId()
	a.audit(u.ID, p.Slug, "release.upload")
	jsonOut(w, 201, map[string]int64{"id": rid})
}
func (a *App) publish(w http.ResponseWriter, r *http.Request) {
	rid := pathID(r)
	var pid int64
	if a.DB.QueryRow("SELECT project_id FROM releases WHERE id=?", rid).Scan(&pid) != nil {
		fail(w, 404, "not_found", "版本不存在")
		return
	}
	u, ok := a.allowed(w, r, pid, false, true)
	if !ok {
		return
	}
	var in struct {
		Published bool `json:"published"`
	}
	if readJSON(w, r, &in) != nil {
		fail(w, 400, "invalid_request", "参数错误")
		return
	}
	p, _ := a.project(pid)
	tx, e := a.DB.Begin()
	if e != nil {
		fail(w, 500, "database_error", "发布失败")
		return
	}
	defer tx.Rollback()
	var old bool
	var payload, sig string
	if tx.QueryRow("SELECT published,payload,signature FROM releases WHERE id=?", rid).Scan(&old, &payload, &sig) != nil {
		fail(w, 500, "database_error", "读取失败")
		return
	}
	if old == in.Published {
		jsonOut(w, 200, map[string]bool{"ok": true})
		return
	}
	if in.Published {
		key, _ := hex.DecodeString(p.PublicKey)
		m, e := (protocol.Envelope{Payload: payload, Signature: sig}).Verify(key)
		if e != nil || m.Expires <= now() {
			fail(w, 400, "expired_manifest", "签名清单已失效，请重新发布新版本")
			return
		}
	}
	if _, e = tx.Exec("UPDATE releases SET published=? WHERE id=?", in.Published, rid); e != nil {
		fail(w, 500, "database_error", "发布失败")
		return
	}
	if in.Published {
		var hook, tg, mqtt string
		_ = tx.QueryRow("SELECT webhook,telegram_token,mqtt_url FROM notifications WHERE project_id=?", pid).Scan(&hook, &tg, &mqtt)
		for kind, value := range map[string]string{"webhook": hook, "telegram": tg, "mqtt": mqtt} {
			if value != "" {
				_, e = tx.Exec("INSERT INTO outbox(project_id,release_id,kind,event,created) VALUES(?,?,?,?,?)", pid, rid, kind, randomToken(), now())
				if e != nil {
					fail(w, 500, "database_error", "通知入队失败")
					return
				}
			}
		}
	} else {
		_, _ = tx.Exec("UPDATE outbox SET state='cancelled',error='release withdrawn' WHERE release_id=? AND state='pending'", rid)
	}
	if tx.Commit() != nil {
		fail(w, 500, "database_error", "发布失败")
		return
	}
	a.audit(u.ID, p.Slug, "release.publish="+strconv.FormatBool(in.Published))
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *App) download(w http.ResponseWriter, r *http.Request) {
	select {
	case a.downloadSlots <- struct{}{}:
		defer func() { <-a.downloadSlots }()
	default:
		w.Header().Set("Retry-After", "10")
		fail(w, 429, "download_busy", "下载并发已满，请稍后重试")
		return
	}
	rid := pathID(r)
	var pid int64
	var published bool
	var hash, version string
	if a.DB.QueryRow("SELECT project_id,published,sha256,version FROM releases WHERE id=?", rid).Scan(&pid, &published, &hash, &version) != nil {
		fail(w, 404, "not_found", "文件不存在")
		return
	}
	p, e := a.project(pid)
	if e != nil {
		fail(w, 404, "not_found", "项目不存在")
		return
	}
	authorized := published && p.Public
	if u, err := a.current(r); err == nil && can(a.role(u, pid), false, false) {
		authorized = true
	}
	if published && a.deviceAuth(r, pid).Valid {
		authorized = true
	}
	if !authorized {
		fail(w, 401, "authentication_required", "请提供项目设备凭据")
		return
	}
	f, e := os.Open(filepath.Join(a.Data, "artifacts", hash))
	if e != nil {
		fail(w, 404, "file_missing", "固件文件缺失，请联系维护者恢复")
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		fail(w, 500, "storage_error", "无法读取固件")
		return
	}
	w.Header().Set("ETag", `"`+hash+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.bin"`, p.Slug, version))
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, version, st.ModTime(), f)
}
func (a *App) public(w http.ResponseWriter, r *http.Request) {
	var id int64
	if a.DB.QueryRow("SELECT id FROM projects WHERE slug=? AND public=1", r.PathValue("slug")).Scan(&id) != nil {
		fail(w, 404, "not_found", "公开项目不存在")
		return
	}
	p, _ := a.project(id)
	rs, ok := a.pagedReleases(w, r, id, true)
	if !ok {
		return
	}
	jsonOut(w, 200, map[string]any{"project": map[string]any{"name": p.Name, "slug": p.Slug, "description": p.Description}, "releases": rs})
}
func (a *App) update(w http.ResponseWriter, r *http.Request) {
	var id int64
	if a.DB.QueryRow("SELECT id FROM projects WHERE slug=?", r.PathValue("slug")).Scan(&id) != nil {
		fail(w, 404, "not_found", "项目不存在")
		return
	}
	p, _ := a.project(id)
	d := a.deviceAuth(r, id)
	u, ue := a.current(r)
	if !p.Public && !d.Valid && (ue != nil || !can(a.role(u, id), false, false)) {
		fail(w, 401, "authentication_required", "需要项目设备凭据")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/releases") {
		rs, ok := a.pagedReleases(w, r, id, true)
		if !ok {
			return
		}
		jsonOut(w, 200, rs)
		return
	}
	q := r.URL.Query()
	current, e := strconv.ParseInt(q.Get("sequence"), 10, 64)
	if (e != nil && q.Get("sequence") != "") || current < 0 {
		fail(w, 400, "invalid_sequence", "版本序号无效")
		return
	}
	security, e := strconv.ParseInt(q.Get("security"), 10, 64)
	if (e != nil && q.Get("security") != "") || security < 0 {
		fail(w, 400, "invalid_security", "安全版本计数无效")
		return
	}
	channel := q.Get("channel")
	if channel == "" {
		channel = "stable"
	}
	key, _ := hex.DecodeString(p.PublicKey)
	chosen, e := a.compatible(id, key, q, current, security, channel)
	if e != nil {
		fail(w, 500, "database_error", "读取兼容版本失败")
		return
	}
	var global bool
	_ = a.DB.QueryRow("SELECT telemetry FROM settings WHERE id=1").Scan(&global)
	available := chosen != nil && chosen.Sequence > current
	// Keep MCU metadata bounded: release notes belong in the history endpoint.
	var compact any
	if chosen != nil {
		compact = map[string]any{"id": chosen.ID, "version": chosen.Version, "sequence": chosen.Sequence, "security": chosen.Security, "size": chosen.Size, "sha256": chosen.SHA256, "payload": chosen.Payload, "signature": chosen.Signature, "download": chosen.Download}
	}
	jsonOut(w, 200, map[string]any{"update": available, "release": compact, "telemetry": map[string]any{"enabled": global && p.Telemetry && d.Valid, "interval_seconds": p.Heartbeat}, "server_time": time.Now().Unix()})
}
