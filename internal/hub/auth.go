package hub

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type user struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Admin bool   `json:"admin"`
	CSRF  string `json:"csrf,omitempty"`
}
type attempt struct {
	Count int
	Until time.Time
}
type loginLimiter struct {
	sync.Mutex
	entries map[string]attempt
}

func newLimiter() *loginLimiter { return &loginLimiter{entries: make(map[string]attempt)} }
func (l *loginLimiter) allow(ip string) bool {
	l.Lock()
	defer l.Unlock()
	t := time.Now()
	if len(l.entries) > 4096 {
		for k, v := range l.entries {
			if t.After(v.Until) {
				delete(l.entries, k)
			}
		}
		if len(l.entries) > 4096 {
			return false
		}
	}
	x := l.entries[ip]
	if t.After(x.Until) {
		x = attempt{Until: t.Add(10 * time.Minute)}
	}
	x.Count++
	l.entries[ip] = x
	return x.Count <= 12
}

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code, msg string) {
	jsonOut(w, status, map[string]string{"error": code, "message": msg})
}
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}
func (a *App) current(r *http.Request) (user, error) {
	var u user
	c, e := r.Cookie("fh_session")
	if e != nil {
		return u, e
	}
	e = a.DB.QueryRow("SELECT u.id,u.name,u.admin,s.csrf FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.hash=? AND s.expires>?", digest(c.Value), now()).Scan(&u.ID, &u.Name, &u.Admin, &u.CSRF)
	if e == nil {
		_, _ = a.DB.Exec("UPDATE session_info SET last_seen=? WHERE hash=? AND last_seen<?", now(), digest(c.Value), now()-60)
	}
	return u, e
}
func (a *App) require(w http.ResponseWriter, r *http.Request) (user, bool) {
	u, e := a.current(r)
	if e != nil {
		fail(w, 401, "authentication_required", "请先登录")
		return u, false
	}
	if r.Method != "GET" && r.Method != "HEAD" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(u.CSRF)) != 1 {
		fail(w, 403, "csrf_failed", "页面已过期，请刷新后重试")
		return u, false
	}
	return u, true
}
func (a *App) role(u user, pid int64) string {
	if u.Admin {
		return "owner"
	}
	var role string
	_ = a.DB.QueryRow("SELECT role FROM members WHERE project_id=? AND user_id=?", pid, u.ID).Scan(&role)
	return role
}
func can(role string, write, publish bool) bool {
	if role == "owner" {
		return true
	}
	if publish {
		return role == "reviewer"
	}
	if write {
		return role == "maintainer"
	}
	return role != ""
}
func (a *App) allowed(w http.ResponseWriter, r *http.Request, pid int64, write, publish bool) (user, bool) {
	u, ok := a.require(w, r)
	if !ok {
		return u, false
	}
	if !can(a.role(u, pid), write, publish) {
		fail(w, 403, "forbidden", "没有此项目的操作权限")
		return u, false
	}
	return u, true
}
func (a *App) auth(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		u, e := a.current(r)
		if e != nil {
			jsonOut(w, 200, map[string]any{"authenticated": false, "version": Version})
			return
		}
		jsonOut(w, 200, map[string]any{"authenticated": true, "user": u, "version": Version})
		return
	}
	if r.Method == "DELETE" {
		u, ok := a.require(w, r)
		if !ok {
			return
		}
		c, _ := r.Cookie("fh_session")
		_, _ = a.DB.Exec("DELETE FROM sessions WHERE hash=?", digest(c.Value))
		http.SetCookie(w, &http.Cookie{Name: "fh_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		a.audit(u.ID, "", "logout")
		jsonOut(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	// Same-host validation works through ordinary HTTPS reverse proxies without
	// trusting forwarded client addresses. Non-browser SDKs do not send Origin.
	if origin := r.Header.Get("Origin"); origin != "" {
		v, e := url.Parse(origin)
		if e != nil || !strings.EqualFold(v.Host, r.Host) {
			fail(w, 403, "origin_mismatch", "登录页面来源不匹配，请从当前站点打开")
			return
		}
	}
	select {
	case a.authSlots <- struct{}{}:
		defer func() { <-a.authSlots }()
	default:
		fail(w, 429, "login_busy", "登录繁忙，请稍后重试")
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !a.login.allow(ip) {
		fail(w, 429, "rate_limited", "尝试次数过多，请稍后重试")
		return
	}
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		OTP      string `json:"otp,omitempty"`
	}
	if readJSON(w, r, &in) != nil || len(in.Password) > 72 {
		fail(w, 400, "invalid_request", "账号或密码格式错误")
		return
	}
	var u user
	var hash string
	e := a.DB.QueryRow("SELECT id,name,password,admin FROM users WHERE name=?", in.Name).Scan(&u.ID, &u.Name, &hash, &u.Admin)
	if e != nil {
		hash = "$2a$12$KbQiHKGT3p6XEOJnB2xeSOclmdZ7nVJ8Wuvh2WzlNXHfn2cKZz.Gm"
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password))
	if e != nil || err != nil {
		a.recordLogin(r, u.ID, in.Name, false, "invalid_credentials")
		fail(w, 401, "invalid_login", "账号或密码不正确")
		return
	}
	var mfaEnabled bool
	if err := a.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM mfa WHERE user_id=? AND secret<>'')", u.ID).Scan(&mfaEnabled); err != nil {
		fail(w, 500, "database_error", "无法确认验证器状态")
		return
	}
	if mfaEnabled && in.OTP == "" {
		fail(w, 401, "mfa_required", "请输入验证器中的六位验证码")
		return
	}
	if !a.loginMFA(u.ID, in.OTP) {
		a.recordLogin(r, u.ID, u.Name, false, "invalid_otp")
		fail(w, 401, "invalid_otp", "校验码无效或已使用，请等待新码并检查系统时间")
		return
	}
	token := randomToken()
	u.CSRF = randomToken()
	_, _ = a.DB.Exec("DELETE FROM sessions WHERE expires<=?", now())
	if _, e = a.DB.Exec("INSERT INTO sessions(hash,user_id,csrf,expires) VALUES(?,?,?,?)", digest(token), u.ID, u.CSRF, now()+7*86400); e != nil {
		fail(w, 500, "database_error", "无法建立会话")
		return
	}
	_, e = a.DB.Exec("INSERT INTO session_info(hash,id,created,last_seen,peer,agent) VALUES(?,?,?,?,?,?)", digest(token), randomToken(), now(), now(), requestPeer(r), shortText(r.UserAgent(), 256))
	if e != nil {
		_, _ = a.DB.Exec("DELETE FROM sessions WHERE hash=?", digest(token))
		fail(w, 500, "database_error", "无法建立会话记录")
		return
	}
	a.recordLogin(r, u.ID, u.Name, true, "ok")
	http.SetCookie(w, &http.Cookie{Name: "fh_session", Value: token, Path: "/", MaxAge: 7 * 86400, HttpOnly: true, Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"), SameSite: http.SameSiteStrictMode})
	a.audit(u.ID, "", "login")
	jsonOut(w, 200, map[string]any{"user": u})
}

// ResetAdmin is a local maintenance operation; all sessions are invalidated.
func ResetAdmin(dir, name, password string) error {
	if name == "" || len(password) < 12 || len(password) > 72 {
		return errors.New("USER and PASSWORD of 12–72 bytes required")
	}
	a, e := Open(dir, name, password)
	if e != nil {
		return e
	}
	defer a.Close()
	h, e := bcrypt.GenerateFromPassword([]byte(password), 12)
	if e != nil {
		return e
	}
	tx, e := a.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO users(name,password,admin) VALUES(?,?,1) ON CONFLICT(name) DO UPDATE SET password=excluded.password,admin=1", name, string(h)); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM sessions"); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM mfa WHERE user_id=(SELECT id FROM users WHERE name=?)", name); e != nil {
		return e
	}
	return tx.Commit()
}
