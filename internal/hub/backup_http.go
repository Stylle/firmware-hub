package hub

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Stylle/firmware-hub/internal/portablebackup"
)

var backupSpec = portablebackup.Spec{App: "firmware-hub", Database: "hub.sqlite", Key: "master.key", Directories: []string{"artifacts"}, Tables: []string{"users", "projects", "releases", "notifications", "sessions"}}

func validateHubBackup(stage string) error {
	db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(stage, "hub.sqlite"))+"?mode=ro")
	if e != nil {
		return e
	}
	defer db.Close()
	key, e := os.ReadFile(filepath.Join(stage, "master.key"))
	if e != nil {
		return e
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return e
	}
	box, e := cipher.NewGCM(block)
	if e != nil {
		return e
	}
	reader := &App{box: box}
	secrets, e := db.Query("SELECT webhook_secret FROM notifications UNION ALL SELECT telegram_token FROM notifications UNION ALL SELECT mqtt_password FROM notifications UNION ALL SELECT secret FROM mfa UNION ALL SELECT pending FROM mfa")
	if e != nil {
		return e
	}
	for secrets.Next() {
		var value string
		if e = secrets.Scan(&value); e != nil {
			secrets.Close()
			return e
		}
		if _, e = reader.unseal(value); e != nil {
			secrets.Close()
			return errors.New("backup key does not match credentials")
		}
	}
	e = secrets.Err()
	secrets.Close()
	if e != nil {
		return e
	}
	var admins int
	if e = db.QueryRow("SELECT COUNT(*) FROM users WHERE admin=1").Scan(&admins); e != nil || admins < 1 {
		return errors.New("missing administrator")
	}
	rows, e := db.Query("SELECT DISTINCT sha256 FROM releases")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		if e = rows.Scan(&hash); e != nil {
			return e
		}
		decoded, decodeErr := hex.DecodeString(hash)
		if len(decoded) != 32 || decodeErr != nil {
			return errors.New("invalid artifact reference")
		}
		artifact, readErr := os.Open(filepath.Join(stage, "artifacts", hash))
		if readErr != nil {
			return errors.New("missing firmware artifact")
		}
		digest := sha256.New()
		_, readErr = io.Copy(digest, artifact)
		artifact.Close()
		if readErr != nil || hex.EncodeToString(digest.Sum(nil)) != hash {
			return errors.New("missing firmware artifact")
		}
	}
	return rows.Err()
}

func (a *App) systemBackup(w http.ResponseWriter, r *http.Request) {
	u, ok := a.require(w, r)
	if !ok {
		return
	}
	if !u.Admin {
		fail(w, 403, "forbidden", "仅管理员可备份和恢复系统")
		return
	}
	if r.Method == http.MethodGet {
		f, e := os.CreateTemp(a.Data, ".download-*.zip")
		if e != nil {
			fail(w, 507, "disk_error", "磁盘无法写入")
			return
		}
		name := f.Name()
		f.Close()
		os.Remove(name)
		defer os.Remove(name)
		if e = portablebackup.Export(a.Data, a.DB, backupSpec, name); e != nil {
			fail(w, 500, "backup_failed", "创建完整备份失败，请检查磁盘和固件文件")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="firmware-hub-`+time.Now().Format("20060102-150405")+`.zip"`)
		http.ServeFile(w, r, name)
		return
	}
	if r.Header.Get("X-Confirm-Restore") != "replace" {
		fail(w, 400, "confirm_required", "请确认恢复将替换当前数据")
		return
	}
	if !a.restoring.CompareAndSwap(false, true) {
		fail(w, 409, "restore_pending", "恢复正在进行")
		return
	}
	accepted := false
	defer func() {
		if !accepted {
			a.restoring.Store(false)
		}
	}()
	r.Body = http.MaxBytesReader(w, r.Body, portablebackup.Limit+32<<20)
	f, e := os.CreateTemp(a.Data, ".import-*.zip")
	if e != nil {
		fail(w, 507, "disk_error", "磁盘无法写入")
		return
	}
	name := f.Name()
	defer os.Remove(name)
	_, e = io.Copy(f, r.Body)
	ce := f.Close()
	if e != nil || ce != nil {
		fail(w, 400, "upload_failed", "备份上传不完整或超过大小限制")
		return
	}
	if e = portablebackup.Stage(a.Data, name, backupSpec, validateHubBackup); e != nil {
		fail(w, 400, "invalid_backup", "备份无效、损坏或不属于固件分发系统，当前数据未修改")
		return
	}
	accepted = true
	jsonOut(w, 200, map[string]any{"ok": true, "relogin": true})
	close(a.Restart)
}
