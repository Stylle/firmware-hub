package hub

import (
	"encoding/json"
	"github.com/pquerna/otp/totp"
	"strings"
	"testing"
	"time"
)

func TestMFAEnrollmentLoginReplayReset(t *testing.T) {
	f := setup(t)
	w := f.call("POST", "/api/v1/mfa", map[string]string{"action": "begin", "password": "TestOnly-Password-1234"}, true, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var enrollment struct {
		Secret string `json:"secret"`
		QR     string `json:"qr"`
	}
	if json.Unmarshal(w.Body.Bytes(), &enrollment) != nil || enrollment.Secret == "" || !strings.HasPrefix(enrollment.QR, "data:image/png;base64,") {
		t.Fatal("enrollment failed")
	}
	w = f.call("POST", "/api/v1/mfa", map[string]string{"action": "confirm", "password": "TestOnly-Password-1234", "code": "bad"}, true, "")
	if w.Code != 400 {
		t.Fatal("invalid enrollment code accepted")
	}
	code, e := totp.GenerateCode(enrollment.Secret, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	w = f.call("POST", "/api/v1/mfa", map[string]string{"action": "confirm", "password": "TestOnly-Password-1234", "code": code}, true, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var sealed string
	_ = f.a.DB.QueryRow("SELECT secret FROM mfa").Scan(&sealed)
	if sealed == enrollment.Secret {
		t.Fatal("TOTP secret stored plaintext")
	}
	if w = f.call("GET", "/api/v1/projects", nil, true, ""); w.Code != 401 {
		t.Fatal("old session not revoked")
	}
	body := map[string]string{"name": "admin", "password": "TestOnly-Password-1234"}
	if w = f.call("POST", "/api/v1/session", body, false, ""); w.Code != 401 {
		t.Fatal("MFA bypassed")
	}
	body["otp"] = code
	if w = f.call("POST", "/api/v1/session", body, false, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = f.call("POST", "/api/v1/session", body, false, ""); w.Code != 401 {
		t.Fatal("TOTP replay accepted")
	}
	if e = ResetAdmin(f.a.Data, "admin", "New-Test-Password-12345"); e != nil {
		t.Fatal(e)
	}
	body = map[string]string{"name": "admin", "password": "New-Test-Password-12345"}
	if w = f.call("POST", "/api/v1/session", body, false, ""); w.Code != 200 {
		t.Fatal("admin recovery failed", w.Body.String())
	}
}
