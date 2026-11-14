package hub

import (
	"encoding/json"
	"github.com/pquerna/otp/totp"
	"testing"
	"time"
)

func TestAuthenticatorReplacementKeepsOldUntilConfirmed(t *testing.T) {
	f := setup(t)
	const old = "JBSWY3DPEHPK3PXP"
	var uid int64
	f.a.DB.QueryRow("SELECT id FROM users WHERE name='admin'").Scan(&uid)
	f.a.DB.Exec("INSERT INTO mfa(user_id,secret) VALUES(?,?)", uid, f.a.seal(old))
	code, _ := totp.GenerateCode(old, time.Now())
	w := f.call("POST", "/api/v1/mfa", map[string]string{"action": "replace", "password": "TestOnly-Password-1234", "code": code}, true, "")
	if w.Code != 200 {
		t.Fatal("begin failed", w.Code)
	}
	var result struct {
		Secret string `json:"secret"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	var sealed string
	f.a.DB.QueryRow("SELECT secret FROM mfa WHERE user_id=?", uid).Scan(&sealed)
	plain, _ := f.a.unseal(sealed)
	if plain != old {
		t.Fatal("old key changed before confirmation")
	}
	if w = f.call("POST", "/api/v1/mfa", map[string]string{"action": "confirm", "password": "TestOnly-Password-1234", "code": "bad"}, true, ""); w.Code != 400 {
		t.Fatal("invalid new proof accepted")
	}
	newCode, _ := totp.GenerateCode(result.Secret, time.Now())
	w = f.call("POST", "/api/v1/mfa", map[string]string{"action": "confirm", "password": "TestOnly-Password-1234", "code": newCode}, true, "")
	if w.Code != 200 {
		t.Fatal("confirm failed", w.Code)
	}
	if f.call("GET", "/api/v1/projects", nil, true, "").Code != 401 {
		t.Fatal("old session survived")
	}
	oldCode, _ := totp.GenerateCode(old, time.Now().Add(30*time.Second))
	if f.call("POST", "/api/v1/session", map[string]string{"name": "admin", "password": "TestOnly-Password-1234", "otp": oldCode}, false, "").Code != 401 {
		t.Fatal("old authenticator survived")
	}
	if f.call("POST", "/api/v1/session", map[string]string{"name": "admin", "password": "TestOnly-Password-1234", "otp": newCode}, false, "").Code != 200 {
		t.Fatal("new authenticator rejected")
	}
}

func TestPasswordChangeRevokesSessions(t *testing.T) {
	f := setup(t)
	if w := f.call("POST", "/api/v1/password", map[string]string{"current_password": "wrong", "new_password": "New-Password-12345"}, true, ""); w.Code != 403 {
		t.Fatal("wrong current password accepted", w.Code)
	}
	if w := f.call("GET", "/api/v1/projects", nil, true, ""); w.Code != 200 {
		t.Fatal("failed change revoked session")
	}
	if w := f.call("POST", "/api/v1/password", map[string]string{"current_password": "TestOnly-Password-1234", "new_password": "New-Password-12345"}, true, ""); w.Code != 200 {
		t.Fatal("password change failed", w.Code)
	}
	if w := f.call("GET", "/api/v1/projects", nil, true, ""); w.Code != 401 {
		t.Fatal("old session survived")
	}
	if w := f.call("POST", "/api/v1/session", map[string]string{"name": "admin", "password": "TestOnly-Password-1234"}, false, ""); w.Code != 401 {
		t.Fatal("old password survived")
	}
	if w := f.call("POST", "/api/v1/session", map[string]string{"name": "admin", "password": "New-Password-12345"}, false, ""); w.Code != 200 {
		t.Fatal("new password rejected")
	}
}
