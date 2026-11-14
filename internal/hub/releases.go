package hub

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
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
	ID          int64  `json:"id"`
	Version     string `json:"version"`
	Sequence    int64  `json:"sequence"`
	Security    int64  `json:"security"`
	Channel     string `json:"channel"`
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	Hardware    string `json:"hardware"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	Payload     string `json:"payload"`
	Signature   string `json:"signature"`
	Notes       string `json:"notes"`
	Published   bool   `json:"published"`
	Created     int64  `json:"created"`
	Download    string `json:"download"`
	FileName    string `json:"file_name"`
	PackageKind string `json:"package_kind"`
	BaseVersion string `json:"base_version"`
	TargetPath  string `json:"target_path"`
	UploadedBy  string `json:"uploaded_by"`
	PublishedBy string `json:"published_by"`
	PublishedAt int64  `json:"published_at"`
}

const releaseCols = "id,version,sequence,security,channel,platform,arch,hardware,sha256,size,payload,signature,notes,published,created," +
	"COALESCE((SELECT file_name FROM release_info WHERE release_id=releases.id),'firmware.bin')," +
	"COALESCE((SELECT package_kind FROM release_info WHERE release_id=releases.id),'full')," +
	"COALESCE((SELECT base_version FROM release_info WHERE release_id=releases.id),'')," +
	"COALESCE((SELECT target_path FROM release_info WHERE release_id=releases.id),'')," +
	"COALESCE((SELECT u.name FROM release_info i JOIN users u ON u.id=i.uploader_id WHERE i.release_id=releases.id),'历史记录')," +
	"COALESCE((SELECT u.name FROM release_info i JOIN users u ON u.id=i.publisher_id WHERE i.release_id=releases.id),'')," +
	"COALESCE((SELECT published_at FROM release_info WHERE release_id=releases.id),0)"

type scanner interface{ Scan(...any) error }

func scanRelease(s scanner) (release, error) {
	var v release
	e := s.Scan(&v.ID, &v.Version, &v.Sequence, &v.Security, &v.Channel, &v.Platform, &v.Arch, &v.Hardware, &v.SHA256, &v.Size, &v.Payload, &v.Signature, &v.Notes, &v.Published, &v.Created, &v.FileName, &v.PackageKind, &v.BaseVersion, &v.TargetPath, &v.UploadedBy, &v.PublishedBy, &v.PublishedAt)
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
		m, e := releaseManifest(protocol.Envelope{Payload: v.Payload, Signature: v.Signature}, key)
		if e == nil && m.Expires > now() && m.MinSequence <= current && (v.PackageKind != "file" || v.BaseVersion == q.Get("version")) {
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
		fail(w, 400, "invalid_upload", "请使用 multipart 上传，顺序为 metadata（或 manifest）、notes、file")
		return
	}
	part, e := mr.NextPart()
	if e != nil || (part.FormName() != "manifest" && part.FormName() != "metadata") {
		fail(w, 400, "metadata_required", "首先提供版本信息 metadata（高级签名模式使用 manifest）")
		return
	}
	b, e := io.ReadAll(io.LimitReader(part, 8193))
	if e != nil || len(b) > 8192 {
		fail(w, 400, "invalid_manifest", "清单过大")
		return
	}
	var env protocol.Envelope
	key, _ := hex.DecodeString(p.PublicKey)
	accountMode := part.FormName() == "metadata"
	meta := releaseMetadata{Channel: "stable", Platform: "any", Arch: "any", Hardware: "any", PackageKind: "full"}
	var m protocol.Manifest
	if accountMode {
		if len(key) != 0 {
			fail(w, 400, "signature_required", "该项目开启高级签名，请上传签名清单，或由所有者在设置中关闭")
			return
		}
		if json.Unmarshal(b, &meta) != nil || !meta.valid() {
			fail(w, 400, "invalid_metadata", "版本信息无效；按文件更新需要填写适用旧版本和相对目标路径")
			return
		}
	} else {
		if json.Unmarshal(b, &env) != nil {
			fail(w, 400, "invalid_manifest", "签名清单 JSON 无效")
			return
		}
		m, e = env.Verify(key)
		if e != nil || m.Project != p.Slug || m.Expires <= now() || m.Size > 1<<30 {
			fail(w, 400, "invalid_signature", "签名、项目、有效期或文件大小不符合要求")
			return
		}
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
	filename := part.FileName()
	if !validPackagePath(filename) || strings.Contains(filename, "/") {
		fail(w, 400, "invalid_filename", "文件名无效，请使用普通文件名")
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
	maxSize := m.Size
	if accountMode {
		maxSize = 1 << 30
	}
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(part, maxSize+1))
	actualHash := hex.EncodeToString(h.Sum(nil))
	if e != nil || n < 1 || n > maxSize || (!accountMode && (n != m.Size || actualHash != m.SHA256)) {
		fail(w, 400, "integrity_error", "文件大小或 SHA-256 与签名清单不一致")
		return
	}
	if accountMode {
		m = protocol.Manifest{Project: p.Slug, Version: meta.Version, Sequence: meta.Sequence, Security: 0, Channel: meta.Channel, Platform: meta.Platform, Arch: meta.Arch, Hardware: meta.Hardware, SHA256: actualHash, Size: n, Expires: 253402300799}
	}
	if _, e = mr.NextPart(); e != io.EOF {
		fail(w, 400, "unexpected_part", "上传包含额外字段")
		return
	}
	if f.Sync() != nil || f.Close() != nil {
		fail(w, 507, "storage_error", "保存文件失败")
		return
	}
	a.artifactMu.Lock()
	defer a.artifactMu.Unlock()
	target := filepath.Join(a.Data, "artifacts", m.SHA256)
	created, committed := false, false
	tx, e := a.DB.Begin()
	if e != nil {
		fail(w, 500, "database_error", "保存版本失败")
		return
	}
	defer func() {
		_ = tx.Rollback()
		if created && !committed {
			var references int
			// The transaction must release SQLite's sole connection first.
			if a.DB.QueryRow("SELECT COUNT(*) FROM releases WHERE sha256=?", m.SHA256).Scan(&references) == nil && references == 0 {
				_ = os.Remove(target)
			}
		}
	}()
	if accountMode {
		if m.Sequence == 0 {
			if tx.QueryRow("SELECT COALESCE(MAX(sequence),0)+1 FROM releases WHERE project_id=?", id).Scan(&m.Sequence) != nil {
				fail(w, 500, "database_error", "生成发布序号失败")
				return
			}
		}
		payload, err := m.Bytes()
		if err != nil {
			fail(w, 400, "invalid_metadata", "版本信息无效")
			return
		}
		env.Payload = base64.StdEncoding.EncodeToString(payload)
	}
	res, e := tx.Exec("INSERT INTO releases(project_id,version,sequence,security,channel,platform,arch,hardware,sha256,size,payload,signature,notes,created) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", id, m.Version, m.Sequence, m.Security, m.Channel, m.Platform, m.Arch, m.Hardware, m.SHA256, m.Size, env.Payload, env.Signature, notes, now())
	if e != nil {
		fail(w, 409, "release_exists", "版本或发布序号已存在，已发布文件不能覆盖")
		return
	}
	rid, _ := res.LastInsertId()
	_, e = tx.Exec("INSERT INTO release_info(release_id,uploader_id,file_name,package_kind,base_version,target_path) VALUES(?,?,?,?,?,?)", rid, u.ID, filename, meta.PackageKind, meta.BaseVersion, meta.TargetPath)
	if e == nil {
		_, e = tx.Exec("INSERT INTO audit(user_id,project,action,at) VALUES(?,?,?,?)", u.ID, p.Slug, fmt.Sprintf("release.upload id=%d version=%s", rid, m.Version), now())
	}
	if e == nil {
		if _, statErr := os.Stat(target); os.IsNotExist(statErr) {
			e = os.Rename(temp, target)
			created = e == nil
		} else if statErr != nil {
			e = statErr
		}
	}
	if e == nil {
		e = tx.Commit()
		committed = e == nil
	}
	if e != nil {
		fail(w, 500, "database_error", "保存发布记录失败")
		return
	}
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
		Published       bool   `json:"published"`
		PublishPassword string `json:"publish_password,omitempty"`
	}
	if readJSON(w, r, &in) != nil {
		fail(w, 400, "invalid_request", "参数错误")
		return
	}
	p, _ := a.project(pid)
	if in.Published && !a.checkPublishPassword(w, r, u, pid, in.PublishPassword) {
		return
	}
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
		m, e := releaseManifest(protocol.Envelope{Payload: payload, Signature: sig}, key)
		if e != nil || m.Expires <= now() {
			fail(w, 400, "expired_manifest", "签名清单已失效，请重新发布新版本")
			return
		}
	}
	if in.Published {
		_, e = tx.Exec("INSERT INTO release_info(release_id,publisher_id,published_at) VALUES(?,?,?) ON CONFLICT(release_id) DO UPDATE SET publisher_id=excluded.publisher_id,published_at=excluded.published_at", rid, u.ID, now())
		if e != nil {
			fail(w, 500, "database_error", "保存发布署名失败")
			return
		}
	}
	if _, e = tx.Exec("UPDATE releases SET published=? WHERE id=?", in.Published, rid); e != nil {
		fail(w, 500, "database_error", "发布失败")
		return
	}
	if in.Published {
		channels, err := notificationFor(tx, pid)
		if err != nil {
			fail(w, 400, "invalid_notification", "通知渠道配置无效，请检查共用项目")
			return
		}
		for kind, value := range map[string]string{"webhook": channels.Webhook, "telegram": channels.TelegramToken, "mqtt": channels.MQTTURL} {
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
	_, e = tx.Exec("INSERT INTO audit(user_id,project,action,at) VALUES(?,?,?,?)", u.ID, p.Slug, fmt.Sprintf("release.publish id=%d published=%t", rid, in.Published), now())
	if e != nil || tx.Commit() != nil {
		fail(w, 500, "database_error", "发布失败")
		return
	}
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
	var hash, version, filename string
	if a.DB.QueryRow("SELECT project_id,published,sha256,version,COALESCE((SELECT file_name FROM release_info WHERE release_id=releases.id),'firmware.bin') FROM releases WHERE id=?", rid).Scan(&pid, &published, &hash, &version, &filename) != nil {
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
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "private, no-cache")
	defer http.NewResponseController(w).SetWriteDeadline(time.Time{})
	http.ServeContent(idleDownloadWriter{ResponseWriter: w, idle: 30 * time.Second}, r, version, st.ModTime(), f)
}
func (a *App) public(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("slug") == "" {
		rows, e := a.DB.Query("SELECT slug,name,description FROM projects WHERE public=1 ORDER BY name LIMIT 500")
		if e != nil {
			fail(w, 500, "database_error", "读取公开项目失败")
			return
		}
		defer rows.Close()
		out := []map[string]string{}
		for rows.Next() {
			var slug, name, description string
			if rows.Scan(&slug, &name, &description) != nil {
				fail(w, 500, "database_error", "读取失败")
				return
			}
			out = append(out, map[string]string{"slug": slug, "name": name, "description": description})
		}
		if rows.Err() != nil {
			fail(w, 500, "database_error", "读取失败")
			return
		}
		jsonOut(w, 200, out)
		return
	}
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
		compact = map[string]any{"id": chosen.ID, "version": chosen.Version, "sequence": chosen.Sequence, "security": chosen.Security, "size": chosen.Size, "sha256": chosen.SHA256, "payload": chosen.Payload, "signature": chosen.Signature, "download": chosen.Download, "file_name": chosen.FileName, "package_kind": chosen.PackageKind, "base_version": chosen.BaseVersion, "target_path": chosen.TargetPath, "published_by": chosen.PublishedBy}
	}
	jsonOut(w, 200, map[string]any{"update": available, "release": compact, "telemetry": map[string]any{"enabled": global && p.Telemetry && d.Valid, "interval_seconds": p.Heartbeat}, "server_time": time.Now().Unix()})
}
