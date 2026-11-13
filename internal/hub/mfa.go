package hub

import (
	"bytes"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"image/png"
	"net/http"
	"strconv"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

func totpStep(secret, code string) int64 {
	if len(code) != 6 {
		return 0
	}
	step := time.Now().Unix() / 30
	for _, offset := range []int64{0, -1, 1} {
		candidate := step + offset
		expected, e := totp.GenerateCode(secret, time.Unix(candidate*30, 0))
		if e == nil && subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 {
			return candidate
		}
	}
	return 0
}
func (a *App) loginMFA(uid int64, code string) bool {
	var sealed string
	var last int64
	e := a.DB.QueryRow("SELECT secret,last_step FROM mfa WHERE user_id=?", uid).Scan(&sealed, &last)
	if e == sql.ErrNoRows {
		return true
	}
	if e != nil {
		return false
	}
	if sealed == "" {
		return true
	}
	secret, e := a.unseal(sealed)
	if e != nil {
		return false
	}
	step := totpStep(secret, code)
	if step == 0 || step <= last {
		return false
	}
	res, e := a.DB.Exec("UPDATE mfa SET last_step=? WHERE user_id=? AND last_step<? AND secret=?", step, uid, step, sealed)
	if e != nil {
		return false
	}
	count, _ := res.RowsAffected()
	return count == 1
}

func (a *App) mfa(w http.ResponseWriter, r *http.Request) {
	u, ok := a.require(w, r)
	if !ok {
		return
	}
	var sealed, pending string
	_ = a.DB.QueryRow("SELECT secret,pending FROM mfa WHERE user_id=?", u.ID).Scan(&sealed, &pending)
	if r.Method == "GET" {
		jsonOut(w, 200, map[string]bool{"enabled": sealed != ""})
		return
	}
	if !a.login.allow("mfa:" + strconv.FormatInt(u.ID, 10)) {
		fail(w, 429, "rate_limited", "尝试次数过多，请稍后重试")
		return
	}
	var in struct {
		Action   string `json:"action"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if readJSON(w, r, &in) != nil || len(in.Password) > 72 {
		fail(w, 400, "invalid_request", "参数错误")
		return
	}
	select {
	case a.authSlots <- struct{}{}:
		defer func() { <-a.authSlots }()
	default:
		fail(w, 429, "login_busy", "认证繁忙，请稍后重试")
		return
	}
	var hash string
	if a.DB.QueryRow("SELECT password FROM users WHERE id=?", u.ID).Scan(&hash) != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		fail(w, 403, "invalid_password", "当前密码不正确")
		return
	}
	if in.Action == "begin" {
		if sealed != "" {
			fail(w, 409, "mfa_enabled", "已启用验证器，请先验证并关闭后再更换")
			return
		}
		key, e := totp.Generate(totp.GenerateOpts{Issuer: "Firmware Hub", AccountName: u.Name, SecretSize: 20})
		if e != nil {
			fail(w, 500, "mfa_error", "生成失败")
			return
		}
		image, e := key.Image(220, 220)
		if e != nil {
			fail(w, 500, "mfa_error", "二维码生成失败")
			return
		}
		var pngBytes bytes.Buffer
		if png.Encode(&pngBytes, image) != nil {
			fail(w, 500, "mfa_error", "二维码生成失败")
			return
		}
		_, e = a.DB.Exec("INSERT INTO mfa(user_id,pending) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET pending=excluded.pending", u.ID, a.seal(key.Secret()))
		if e != nil {
			fail(w, 500, "database_error", "保存失败")
			return
		}
		jsonOut(w, 200, map[string]string{"secret": key.Secret(), "qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes.Bytes())})
		return
	}
	var query string
	var args []any
	if in.Action == "confirm" {
		secret, e := a.unseal(pending)
		if e != nil || secret == "" || sealed != "" || totpStep(secret, in.Code) == 0 {
			fail(w, 400, "invalid_otp", "校验码无效，请检查验证器和系统时间")
			return
		}
		query = "UPDATE mfa SET secret=pending,pending='',last_step=0 WHERE user_id=? AND pending=? AND secret=''"
		args = []any{u.ID, pending}
	} else if in.Action == "disable" {
		secret, e := a.unseal(sealed)
		if e != nil || secret == "" || totpStep(secret, in.Code) == 0 {
			fail(w, 400, "invalid_otp", "请提供有效的当前校验码")
			return
		}
		query = "DELETE FROM mfa WHERE user_id=? AND secret=?"
		args = []any{u.ID, sealed}
	} else {
		fail(w, 400, "invalid_action", "操作无效")
		return
	}
	tx, e := a.DB.Begin()
	if e != nil {
		fail(w, 500, "database_error", "保存失败")
		return
	}
	defer tx.Rollback()
	res, e := tx.Exec(query, args...)
	if e != nil {
		fail(w, 500, "database_error", "保存失败")
		return
	}
	count, _ := res.RowsAffected()
	if count != 1 {
		fail(w, 409, "mfa_changed", "绑定状态已变化，请刷新后重试")
		return
	}
	if _, e = tx.Exec("DELETE FROM sessions WHERE user_id=?", u.ID); e != nil {
		fail(w, 500, "database_error", "无法撤销会话")
		return
	}
	if _, e = tx.Exec("INSERT INTO audit(user_id,project,action,at) VALUES(?,'',?,?)", u.ID, "mfa."+in.Action, now()); e != nil {
		fail(w, 500, "database_error", "无法保存审计记录")
		return
	}
	if e = tx.Commit(); e != nil {
		fail(w, 500, "database_error", "保存失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "fh_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	jsonOut(w, 200, map[string]bool{"ok": true, "reauthenticate": true})
}
