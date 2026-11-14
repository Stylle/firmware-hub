package hub

import (
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strconv"
	"unicode/utf8"
)

func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	a.credentialMu.Lock()
	defer a.credentialMu.Unlock()
	u, ok := a.require(w, r)
	if !ok {
		return
	}
	if !a.login.allow("password:" + strconv.FormatInt(u.ID, 10)) {
		fail(w, 429, "rate_limited", "尝试次数过多，请稍后重试")
		return
	}
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
		OTP     string `json:"otp"`
	}
	if readJSON(w, r, &in) != nil || len(in.New) < 12 || len(in.New) > 72 || !utf8.ValidString(in.New) || in.Current == in.New {
		fail(w, 400, "invalid_password", "新密码需要 12–72 字节，且不能与原密码相同")
		return
	}
	if !a.reauthenticate(w, r, u, in.Current) {
		return
	}
	if !a.loginMFA(u.ID, in.OTP) {
		fail(w, 403, "invalid_otp", "请提供有效且未使用的当前验证码")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.New), 12)
	if err != nil {
		fail(w, 500, "password_error", "修改失败")
		return
	}
	tx, err := a.DB.Begin()
	if err != nil {
		fail(w, 500, "database_error", "修改失败")
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE users SET password=? WHERE id=?", string(hash), u.ID); err == nil {
		_, err = tx.Exec("DELETE FROM sessions WHERE user_id=?", u.ID)
	}
	if err == nil {
		_, err = tx.Exec("INSERT INTO audit(user_id,project,action,at) VALUES(?,'','password.change',?)", u.ID, now())
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 500, "database_error", "修改失败，原密码未改变")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "fh_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: requestHTTPS(r), SameSite: http.SameSiteStrictMode})
	jsonOut(w, 200, map[string]bool{"ok": true, "reauthenticate": true})
}
