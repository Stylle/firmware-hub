package hub

import (
	"database/sql"
	"golang.org/x/crypto/bcrypt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

func shortText(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		r = r[:max]
	}
	return string(r)
}

// Forwarded identities and HTTPS require an explicitly trusted proxy peer.
func trustedProxy(r *http.Request) bool {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return false
	}
	ip := net.ParseIP(host)
	for _, value := range strings.Split(os.Getenv("TRUSTED_PROXIES"), ",") {
		value = strings.TrimSpace(value)
		if parsed := net.ParseIP(value); parsed != nil && parsed.Equal(ip) {
			return true
		}
		if _, block, e := net.ParseCIDR(value); e == nil && block.Contains(ip) {
			return true
		}
	}
	return false
}
func requestHTTPS(r *http.Request) bool {
	return r.TLS != nil || os.Getenv("PUBLIC_HTTPS") == "1" || trustedProxy(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
func requestPeer(r *http.Request) string {
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		return "unknown"
	}
	if trustedProxy(r) {
		if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
			return ip.String()
		}
	}
	return host
}
func (a *App) recordLogin(r *http.Request, id int64, name string, success bool, reason string) {
	var uid any
	if id != 0 {
		uid = id
	}
	_, _ = a.DB.Exec("INSERT INTO login_history(user_id,name,success,reason,peer,agent,at) VALUES(?,?,?,?,?,?,?)", uid, shortText(name, 64), success, reason, requestPeer(r), shortText(r.UserAgent(), 256), now())
	// Bounded retention also prevents unlimited growth from failed sign-ins.
	_, _ = a.DB.Exec("DELETE FROM login_history WHERE at<? OR id NOT IN (SELECT id FROM login_history ORDER BY id DESC LIMIT 5000)", now()-90*86400)
}
func (a *App) reauthenticate(w http.ResponseWriter, r *http.Request, u user, password string) bool {
	if len(password) > 72 || !utf8.ValidString(password) {
		fail(w, 400, "invalid_password", "密码格式错误")
		return false
	}
	select {
	case a.authSlots <- struct{}{}:
		defer func() { <-a.authSlots }()
	default:
		fail(w, 429, "busy", "验证繁忙，请稍后重试")
		return false
	}
	if !a.login.allow("reauth:" + strconv.FormatInt(u.ID, 10)) {
		fail(w, 429, "rate_limited", "尝试过多，请稍后重试")
		return false
	}
	var hash string
	if a.DB.QueryRow("SELECT password FROM users WHERE id=?", u.ID).Scan(&hash) != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		fail(w, 403, "password_failed", "当前账号密码不正确")
		return false
	}
	return true
}
func (a *App) activity(w http.ResponseWriter, r *http.Request) {
	u, ok := a.require(w, r)
	if !ok {
		return
	}
	all := u.Admin && r.URL.Query().Get("all") == "1"
	where := " WHERE user_id=?"
	args := []any{u.ID}
	if all {
		where = ""
		args = nil
	}
	rows, e := a.DB.Query("SELECT id,name,success,reason,peer,agent,at FROM login_history"+where+" ORDER BY id DESC LIMIT 100", args...)
	if e != nil {
		fail(w, 500, "database_error", "读取登录历史失败")
		return
	}
	history := []map[string]any{}
	for rows.Next() {
		var id, at int64
		var name, reason, peer, agent string
		var success bool
		if e = rows.Scan(&id, &name, &success, &reason, &peer, &agent, &at); e != nil {
			break
		}
		history = append(history, map[string]any{"id": id, "name": name, "success": success, "reason": reason, "peer": peer, "agent": agent, "at": at})
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		fail(w, 500, "database_error", "读取登录历史失败")
		return
	}
	q := "SELECT i.id,u.name,i.created,i.last_seen,s.expires,i.peer,i.agent,s.hash FROM sessions s JOIN users u ON u.id=s.user_id JOIN session_info i ON i.hash=s.hash WHERE s.expires>?"
	args = []any{now()}
	if !all {
		q += " AND s.user_id=?"
		args = append(args, u.ID)
	}
	q += " ORDER BY i.last_seen DESC LIMIT 200"
	rows, e = a.DB.Query(q, args...)
	if e != nil {
		fail(w, 500, "database_error", "读取会话失败")
		return
	}
	defer rows.Close()
	sessions := []map[string]any{}
	current := ""
	if c, e := r.Cookie("fh_session"); e == nil {
		current = digest(c.Value)
	}
	for rows.Next() {
		var id, name, peer, agent, hash string
		var created, seen, expires int64
		if rows.Scan(&id, &name, &created, &seen, &expires, &peer, &agent, &hash) != nil {
			fail(w, 500, "database_error", "读取会话失败")
			return
		}
		sessions = append(sessions, map[string]any{"id": id, "name": name, "created": created, "last_seen": seen, "expires": expires, "peer": peer, "agent": agent, "current": hash == current, "active": seen > now()-900})
	}
	if rows.Err() != nil {
		fail(w, 500, "database_error", "读取会话失败")
		return
	}
	jsonOut(w, 200, map[string]any{"history": history, "sessions": sessions})
}
func (a *App) revokeSession(w http.ResponseWriter, r *http.Request) {
	u, ok := a.require(w, r)
	if !ok {
		return
	}
	var uid int64
	var hash string
	if e := a.DB.QueryRow("SELECT s.user_id,s.hash FROM sessions s JOIN session_info i ON i.hash=s.hash WHERE i.id=?", r.PathValue("id")).Scan(&uid, &hash); e != nil {
		if e == sql.ErrNoRows {
			fail(w, 404, "not_found", "会话不存在")
		} else {
			fail(w, 500, "database_error", "读取失败")
		}
		return
	}
	if uid != u.ID && !u.Admin {
		fail(w, 403, "forbidden", "不能撤销其他用户会话")
		return
	}
	if _, e := a.DB.Exec("DELETE FROM sessions WHERE hash=?", hash); e != nil {
		fail(w, 500, "database_error", "撤销失败")
		return
	}
	a.audit(u.ID, "", "session.revoke user="+strconv.FormatInt(uid, 10))
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *App) projectAudit(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	_, ok := a.allowed(w, r, id, false, false)
	if !ok {
		return
	}
	p, e := a.project(id)
	if e != nil {
		fail(w, 404, "not_found", "项目不存在")
		return
	}
	rows, e := a.DB.Query("SELECT a.id,COALESCE(u.name,'unknown'),a.action,a.at FROM audit a LEFT JOIN users u ON u.id=a.user_id WHERE a.project=? ORDER BY a.id DESC LIMIT 200", p.Slug)
	if e != nil {
		fail(w, 500, "database_error", "读取审计记录失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, at int64
		var name, action string
		if rows.Scan(&id, &name, &action, &at) != nil {
			fail(w, 500, "database_error", "读取失败")
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "action": action, "at": at})
	}
	if rows.Err() != nil {
		fail(w, 500, "database_error", "读取失败")
		return
	}
	jsonOut(w, 200, out)
}
