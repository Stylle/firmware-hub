package hub

import (
	"database/sql"
	"encoding/base64"
	"github.com/Stylle/firmware-hub/internal/protocol"
	"github.com/Stylle/firmware-hub/internal/updatepath"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strconv"
)

// Account mode deliberately trusts the authenticated Hub over HTTPS. It is not
// publisher authentication if the Hub itself is compromised. Signed clients
// remain fail-closed and never use this fallback implicitly.
func releaseManifest(env protocol.Envelope, key []byte) (protocol.Manifest, error) {
	if len(key) != 0 {
		return env.Verify(key)
	}
	b, e := base64.StdEncoding.Strict().DecodeString(env.Payload)
	if e != nil {
		return protocol.Manifest{}, e
	}
	return protocol.Parse(b)
}

type releaseMetadata struct {
	Version     string `json:"version"`
	Sequence    int64  `json:"sequence"`
	Channel     string `json:"channel"`
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	Hardware    string `json:"hardware"`
	PackageKind string `json:"package_kind"`
	BaseVersion string `json:"base_version"`
	TargetPath  string `json:"target_path"`
}

func (m releaseMetadata) valid() bool {
	for _, s := range []string{m.Version, m.Channel, m.Platform, m.Arch, m.Hardware} {
		if !protocol.Identifier.MatchString(s) {
			return false
		}
	}
	if m.Sequence < 0 {
		return false
	}
	if m.PackageKind == "full" {
		return m.BaseVersion == "" && m.TargetPath == ""
	}
	return m.PackageKind == "file" && protocol.Identifier.MatchString(m.BaseVersion) && m.BaseVersion != m.Version && validPackagePath(m.TargetPath)
}

// Restrict paths to portable relative regular files; never accept Windows drive,
// UNC, alternate streams, reserved device names or parent traversal.
func validPackagePath(s string) bool {
	return updatepath.Valid(s)
}
func (a *App) checkPublishPassword(w http.ResponseWriter, r *http.Request, u user, pid int64, password string) bool {
	var hash string
	err := a.DB.QueryRow("SELECT publish_password FROM project_security WHERE project_id=?", pid).Scan(&hash)
	if err != nil && err != sql.ErrNoRows {
		fail(w, 500, "database_error", "读取发布保护失败")
		return false
	}
	if hash == "" {
		return true
	}
	if len(password) > 72 {
		fail(w, 403, "publish_password_failed", "发布密码不正确")
		return false
	}
	select {
	case a.authSlots <- struct{}{}:
		defer func() { <-a.authSlots }()
	default:
		fail(w, 429, "busy", "验证繁忙，请稍后重试")
		return false
	}
	if !a.login.allow("publish:" + strconv.FormatInt(u.ID, 10) + ":" + strconv.FormatInt(pid, 10)) {
		fail(w, 429, "rate_limited", "发布验证次数过多，请稍后重试")
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		a.auditProject(u.ID, pid, "release.publish_password_failed")
		fail(w, 403, "publish_password_failed", "发布密码不正确")
		return false
	}
	return true
}
