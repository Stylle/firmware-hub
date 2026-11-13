package hub

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Stylle/firmware-hub/internal/protocol"
	"golang.org/x/crypto/bcrypt"
)

type project struct {
	ID                 int64   `json:"id"`
	Slug               string  `json:"slug"`
	Name               string  `json:"name"`
	Description        string  `json:"description"`
	Public             bool    `json:"public"`
	Telemetry          bool    `json:"telemetry"`
	Heartbeat          int     `json:"heartbeat"`
	Timeout            int     `json:"timeout"`
	PublicKey          string  `json:"public_key"`
	Role               string  `json:"role"`
	HasPublishPassword bool    `json:"has_publish_password"`
	PublishPassword    *string `json:"publish_password,omitempty"`
	AccountPassword    string  `json:"account_password,omitempty"`
}

func (a *App) project(id int64) (project, error) {
	var p project
	e := a.DB.QueryRow("SELECT id,slug,name,description,public,telemetry,heartbeat,timeout,pubkey FROM projects WHERE id=?", id).Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.Public, &p.Telemetry, &p.Heartbeat, &p.Timeout, &p.PublicKey)
	if e == nil {
		_ = a.DB.QueryRow("SELECT publish_password!='' FROM project_security WHERE project_id=?", id).Scan(&p.HasPublishPassword)
	}
	return p, e
}
func pathID(r *http.Request) int64 { n, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return n }
func (a *App) projects(w http.ResponseWriter, r *http.Request) {
	u, ok := a.require(w, r)
	if !ok {
		return
	}
	if r.Method == "GET" {
		rows, e := a.DB.Query("SELECT p.id,p.slug,p.name,p.description,p.public,p.telemetry,p.heartbeat,p.timeout,p.pubkey,CASE WHEN ? THEN 'owner' ELSE m.role END,COALESCE((SELECT publish_password!='' FROM project_security WHERE project_id=p.id),0) FROM projects p LEFT JOIN members m ON m.project_id=p.id AND m.user_id=? WHERE ? OR m.user_id=? ORDER BY p.id DESC", u.Admin, u.ID, u.Admin, u.ID)
		if e != nil {
			fail(w, 500, "database_error", "读取项目失败")
			return
		}
		defer rows.Close()
		out := []project{}
		for rows.Next() {
			var p project
			if rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.Public, &p.Telemetry, &p.Heartbeat, &p.Timeout, &p.PublicKey, &p.Role, &p.HasPublishPassword) != nil {
				fail(w, 500, "database_error", "读取项目失败")
				return
			}
			out = append(out, p)
		}
		jsonOut(w, 200, out)
		return
	}
	var p project
	if readJSON(w, r, &p) != nil || !validProject(p) {
		fail(w, 400, "invalid_project", "请填写名称、英文项目标识，并检查心跳参数；公钥可不填")
		return
	}
	hash, e := hashPublishPassword(p.PublishPassword)
	if e != nil {
		fail(w, 400, "invalid_password", e.Error())
		return
	}
	p.HasPublishPassword=hash!=""
	tx, e := a.DB.Begin()
	if e != nil {
		fail(w, 500, "database_error", "创建失败")
		return
	}
	defer tx.Rollback()
	res, e := tx.Exec("INSERT INTO projects(slug,name,description,owner,public,telemetry,heartbeat,timeout,pubkey) VALUES(?,?,?,?,?,?,?,?,?)", p.Slug, p.Name, p.Description, u.ID, p.Public, p.Telemetry, p.Heartbeat, p.Timeout, p.PublicKey)
	if e != nil {
		fail(w, 409, "project_exists", "项目标识已存在")
		return
	}
	p.ID, _ = res.LastInsertId()
	_, e = tx.Exec("INSERT INTO members(project_id,user_id,role) VALUES(?,?,'owner')", p.ID, u.ID)
	if e == nil && hash != "" {
		_, e = tx.Exec("INSERT INTO project_security(project_id,publish_password) VALUES(?,?)", p.ID, hash)
		p.HasPublishPassword = true
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		fail(w, 500, "database_error", "创建失败")
		return
	}
	a.audit(u.ID, p.Slug, "project.create")
	p.Role = "owner"
	p.PublishPassword = nil
	p.AccountPassword = ""
	jsonOut(w, 201, p)
}
func validProject(p project) bool {
	k, e := hex.DecodeString(p.PublicKey)
	return protocol.Identifier.MatchString(p.Slug) && len(p.Name) > 0 && utf8.RuneCountInString(p.Name) <= 100 && utf8.RuneCountInString(p.Description) <= 4000 && (p.PublicKey == "" || (e == nil && len(k) == 32)) && p.Heartbeat >= 15 && p.Heartbeat <= 86400 && p.Timeout >= p.Heartbeat*2 && p.Timeout <= 7*86400
}
func (a *App) updateProject(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	u, ok := a.allowed(w, r, id, true, false)
	if !ok {
		return
	}
	if a.role(u, id) != "owner" {
		fail(w, 403, "forbidden", "仅项目所有者可修改设置")
		return
	}
	old, e := a.project(id)
	if e != nil {
		fail(w, 404, "not_found", "项目不存在")
		return
	}
	var p project
	if readJSON(w, r, &p) != nil || !validProject(p) || p.Slug != old.Slug || (p.PublicKey != old.PublicKey && p.PublicKey != "") {
		fail(w, 400, "immutable_trust", "项目标识和已有信任公钥不可替换；关闭签名须验证当前账号密码，原验签客户端不会自动降级")
		return
	}
	securityChange := p.PublicKey != old.PublicKey || p.PublishPassword != nil
	if securityChange && !a.reauthenticate(w, r, u, p.AccountPassword) {
		return
	}
	hash, e := hashPublishPassword(p.PublishPassword)
	if e != nil {
		fail(w, 400, "invalid_password", e.Error())
		return
	}
	tx, e := a.DB.Begin()
	if e != nil {
		fail(w, 500, "database_error", "保存失败")
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec("UPDATE projects SET name=?,description=?,public=?,telemetry=?,heartbeat=?,timeout=?,pubkey=? WHERE id=?", p.Name, p.Description, p.Public, p.Telemetry, p.Heartbeat, p.Timeout, p.PublicKey, id)
	if e == nil && p.PublishPassword != nil {
		_, e = tx.Exec("INSERT INTO project_security(project_id,publish_password) VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET publish_password=excluded.publish_password", id, hash)
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		fail(w, 500, "database_error", "保存失败")
		return
	}
	a.audit(u.ID, p.Slug, "project.update")
	if securityChange {
		a.audit(u.ID, p.Slug, "project.release_security.update")
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func hashPublishPassword(password *string) (string, error) {
	if password == nil || *password == "" {
		return "", nil
	}
	if len(*password) < 12 || len(*password) > 72 || strings.TrimSpace(*password) == "" {
		return "", fmt.Errorf("发布密码必须是 12–72 字节，留空表示不启用")
	}
	h, e := bcrypt.GenerateFromPassword([]byte(*password), 12)
	return string(h), e
}
func (a *App) users(w http.ResponseWriter, r *http.Request) {
	u, ok := a.require(w, r)
	if !ok {
		return
	}
	if !u.Admin {
		fail(w, 403, "forbidden", "仅管理员可操作")
		return
	}
	if r.Method == "GET" {
		rows, e := a.DB.Query("SELECT id,name,admin FROM users ORDER BY id")
		if e != nil {
			fail(w, 500, "database_error", "读取失败")
			return
		}
		defer rows.Close()
		out := []user{}
		for rows.Next() {
			var v user
			_ = rows.Scan(&v.ID, &v.Name, &v.Admin)
			out = append(out, v)
		}
		jsonOut(w, 200, out)
		return
	}
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Admin    bool   `json:"admin"`
	}
	if readJSON(w, r, &in) != nil || !protocol.Identifier.MatchString(in.Name) || len(in.Password) < 12 || len(in.Password) > 72 {
		fail(w, 400, "invalid_user", "用户名使用英文标识，密码为 12–72 字节")
		return
	}
	h, e := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if e != nil {
		fail(w, 500, "hash_error", "创建失败")
		return
	}
	if _, e = a.DB.Exec("INSERT INTO users(name,password,admin) VALUES(?,?,?)", in.Name, string(h), in.Admin); e != nil {
		fail(w, 409, "user_exists", "用户名已存在")
		return
	}
	a.audit(u.ID, "", "user.create")
	jsonOut(w, 201, map[string]bool{"ok": true})
}
func (a *App) members(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	u, ok := a.allowed(w, r, id, false, false)
	if !ok {
		return
	}
	if r.Method == "GET" {
		rows, e := a.DB.Query("SELECT u.id,u.name,m.role FROM members m JOIN users u ON u.id=m.user_id WHERE m.project_id=? ORDER BY u.id", id)
		if e != nil {
			fail(w, 500, "database_error", "读取失败")
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var uid int64
			var name, role string
			_ = rows.Scan(&uid, &name, &role)
			out = append(out, map[string]any{"id": uid, "name": name, "role": role})
		}
		jsonOut(w, 200, out)
		return
	}
	if a.role(u, id) != "owner" {
		fail(w, 403, "forbidden", "仅所有者可管理成员")
		return
	}
	var in struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if readJSON(w, r, &in) != nil || (in.Role != "maintainer" && in.Role != "reviewer" && in.Role != "viewer" && in.Role != "remove") {
		fail(w, 400, "invalid_role", "成员角色无效")
		return
	}
	var uid int64
	var role string
	if a.DB.QueryRow("SELECT id FROM users WHERE name=?", in.Name).Scan(&uid) != nil {
		fail(w, 404, "user_not_found", "请先由管理员创建用户")
		return
	}
	e := a.DB.QueryRow("SELECT role FROM members WHERE project_id=? AND user_id=?", id, uid).Scan(&role)
	if e != nil && e != sql.ErrNoRows {
		fail(w, 500, "database_error", "保存失败")
		return
	}
	if role == "owner" {
		fail(w, 400, "owner_protected", "不能修改项目所有者")
		return
	}
	if in.Role == "remove" {
		_, e = a.DB.Exec("DELETE FROM members WHERE project_id=? AND user_id=?", id, uid)
	} else {
		_, e = a.DB.Exec("INSERT INTO members VALUES(?,?,?) ON CONFLICT(project_id,user_id) DO UPDATE SET role=excluded.role", id, uid, in.Role)
	}
	if e != nil {
		fail(w, 500, "database_error", "保存失败")
		return
	}
	a.auditProject(u.ID, id, "member.update")
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	u, ok := a.require(w, r)
	if !ok {
		return
	}
	var s struct {
		Telemetry bool `json:"telemetry"`
	}
	if r.Method == "GET" {
		_ = a.DB.QueryRow("SELECT telemetry FROM settings WHERE id=1").Scan(&s.Telemetry)
		jsonOut(w, 200, s)
		return
	}
	if !u.Admin {
		fail(w, 403, "forbidden", "仅管理员可修改全局采集开关")
		return
	}
	if readJSON(w, r, &s) != nil {
		fail(w, 400, "invalid_request", "参数错误")
		return
	}
	_, e := a.DB.Exec("UPDATE settings SET telemetry=? WHERE id=1", s.Telemetry)
	if e != nil {
		fail(w, 500, "database_error", "保存失败")
		return
	}
	a.audit(u.ID, "", "telemetry.global.update")
	jsonOut(w, 200, s)
}
