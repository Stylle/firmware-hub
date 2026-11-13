// Package hub implements the authenticated firmware distribution service.
// Firmware signing is intentionally outside the service's trust boundary.
package hub

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const Version = "0.4.1"

type App struct {
	DB            *sql.DB
	Data          string
	box           cipher.AEAD
	login         *loginLimiter
	authSlots     chan struct{}
	uploadSlots   chan struct{}
	downloadSlots chan struct{}
}

func Open(dir, username, password string) (*App, error) {
	if err := os.MkdirAll(filepath.Join(dir, "artifacts"), 0700); err != nil {
		return nil, fmt.Errorf("data directory is not writable: %w", err)
	}
	keyPath := filepath.Join(dir, "master.key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		_, err = f.Write(key)
		if e = f.Close(); err == nil {
			err = e
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("invalid data/master.key; restore the matching backup")
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	box, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "hub.sqlite"))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	a := &App{DB: db, Data: dir, box: box, login: newLimiter(), authSlots: make(chan struct{}, 2), uploadSlots: make(chan struct{}, 2), downloadSlots: make(chan struct{}, 16)}
	if _, e = db.Exec(schema); e != nil {
		db.Close()
		return nil, e
	}
	if e = os.Chmod(filepath.Join(dir, "hub.sqlite"), 0600); e != nil {
		db.Close()
		return nil, e
	}
	var count int
	if e = db.QueryRow("SELECT count(*) FROM users").Scan(&count); e != nil {
		db.Close()
		return nil, e
	}
	if count == 0 {
		if username == "" || len(password) < 12 || len(password) > 72 || strings.Contains(password, "请替换") {
			db.Close()
			return nil, errors.New("first start requires USER and PASSWORD (12–72 bytes); later starts reuse the saved account")
		}
		hash, e := bcrypt.GenerateFromPassword([]byte(password), 12)
		if e != nil {
			db.Close()
			return nil, e
		}
		if _, e = db.Exec("INSERT INTO users(name,password,admin) VALUES(?,?,1)", username, string(hash)); e != nil {
			db.Close()
			return nil, e
		}
	}
	return a, nil
}

func (a *App) Close() error { return a.DB.Close() }

func (a *App) seal(s string) string {
	if s == "" {
		return ""
	}
	n := make([]byte, a.box.NonceSize())
	if _, e := rand.Read(n); e != nil {
		panic(e)
	}
	return hex.EncodeToString(a.box.Seal(n, n, []byte(s), nil))
}
func (a *App) unseal(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	b, e := hex.DecodeString(s)
	if e != nil || len(b) < a.box.NonceSize() {
		return "", errors.New("invalid encrypted credential")
	}
	p, e := a.box.Open(nil, b[:a.box.NonceSize()], b[a.box.NonceSize():], nil)
	return string(p), e
}
func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func now() int64             { return time.Now().Unix() }
func (a *App) audit(uid int64, project, action string) {
	_, _ = a.DB.Exec("INSERT INTO audit(user_id,project,action,at) VALUES(?,?,?,?)", uid, project, action, now())
}
func (a *App) auditProject(uid, pid int64, action string) {
	var slug string
	if a.DB.QueryRow("SELECT slug FROM projects WHERE id=?", pid).Scan(&slug) == nil {
		a.audit(uid, slug, action)
	}
}

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY,name TEXT NOT NULL UNIQUE,password TEXT NOT NULL,admin INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS sessions(hash TEXT PRIMARY KEY,user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,csrf TEXT NOT NULL,expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS mfa(user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,secret TEXT NOT NULL DEFAULT '',pending TEXT NOT NULL DEFAULT '',last_step INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS settings(id INTEGER PRIMARY KEY CHECK(id=1),telemetry INTEGER NOT NULL DEFAULT 1,registration INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO settings(id) VALUES(1);
CREATE TABLE IF NOT EXISTS projects(id INTEGER PRIMARY KEY,slug TEXT UNIQUE NOT NULL,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',owner INTEGER NOT NULL REFERENCES users(id),public INTEGER NOT NULL DEFAULT 0,telemetry INTEGER NOT NULL DEFAULT 0,heartbeat INTEGER NOT NULL DEFAULT 60,timeout INTEGER NOT NULL DEFAULT 180,pubkey TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS members(project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,role TEXT NOT NULL CHECK(role IN ('owner','maintainer','reviewer','viewer')),PRIMARY KEY(project_id,user_id));
CREATE TABLE IF NOT EXISTS releases(id INTEGER PRIMARY KEY,project_id INTEGER NOT NULL REFERENCES projects(id),version TEXT NOT NULL,sequence INTEGER NOT NULL,security INTEGER NOT NULL,channel TEXT NOT NULL,platform TEXT NOT NULL,arch TEXT NOT NULL,hardware TEXT NOT NULL,sha256 TEXT NOT NULL,size INTEGER NOT NULL,payload TEXT NOT NULL,signature TEXT NOT NULL,notes TEXT NOT NULL DEFAULT '',published INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL,UNIQUE(project_id,sequence),UNIQUE(project_id,version,channel,platform,arch,hardware));
CREATE TABLE IF NOT EXISTS devices(id INTEGER PRIMARY KEY,project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,name TEXT NOT NULL,token_hash TEXT NOT NULL UNIQUE,revoked INTEGER NOT NULL DEFAULT 0,version TEXT NOT NULL DEFAULT '',platform TEXT NOT NULL DEFAULT '',arch TEXT NOT NULL DEFAULT '',hardware TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'unknown',last_seen INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS notifications(id INTEGER PRIMARY KEY,project_id INTEGER NOT NULL UNIQUE REFERENCES projects(id) ON DELETE CASCADE,webhook TEXT NOT NULL DEFAULT '',webhook_secret TEXT NOT NULL DEFAULT '',telegram_token TEXT NOT NULL DEFAULT '',telegram_chat TEXT NOT NULL DEFAULT '',mqtt_url TEXT NOT NULL DEFAULT '',mqtt_user TEXT NOT NULL DEFAULT '',mqtt_password TEXT NOT NULL DEFAULT '',mqtt_topic TEXT NOT NULL DEFAULT '',allow_private INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS notification_options(project_id INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,webhook_enabled INTEGER NOT NULL DEFAULT 1,telegram_enabled INTEGER NOT NULL DEFAULT 1,mqtt_enabled INTEGER NOT NULL DEFAULT 1,webhook_source INTEGER NOT NULL DEFAULT 0,telegram_source INTEGER NOT NULL DEFAULT 0,mqtt_source INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS outbox(id INTEGER PRIMARY KEY,project_id INTEGER NOT NULL,release_id INTEGER NOT NULL,kind TEXT NOT NULL,event TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_at INTEGER NOT NULL DEFAULT 0,error TEXT NOT NULL DEFAULT '',created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS audit(id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL,project TEXT NOT NULL,action TEXT NOT NULL,at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS project_security(project_id INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,publish_password TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS release_info(release_id INTEGER PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,uploader_id INTEGER REFERENCES users(id),publisher_id INTEGER REFERENCES users(id),published_at INTEGER NOT NULL DEFAULT 0,file_name TEXT NOT NULL DEFAULT 'download.bin',package_kind TEXT NOT NULL DEFAULT 'full',base_version TEXT NOT NULL DEFAULT '',target_path TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS login_history(id INTEGER PRIMARY KEY,user_id INTEGER REFERENCES users(id),name TEXT NOT NULL,success INTEGER NOT NULL,reason TEXT NOT NULL,peer TEXT NOT NULL,agent TEXT NOT NULL,at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS session_info(hash TEXT PRIMARY KEY REFERENCES sessions(hash) ON DELETE CASCADE,id TEXT NOT NULL UNIQUE,created INTEGER NOT NULL,last_seen INTEGER NOT NULL,peer TEXT NOT NULL,agent TEXT NOT NULL);
-- Public management IDs are not login tokens. Existing sessions remain usable
-- across this additive migration, with unknown pre-upgrade client metadata.
INSERT OR IGNORE INTO session_info(hash,id,created,last_seen,peer,agent) SELECT hash,lower(hex(randomblob(16))),expires-604800,0,'升级前会话','未知（升级前）' FROM sessions WHERE expires>strftime('%s','now');
CREATE INDEX IF NOT EXISTS login_history_user ON login_history(user_id,id DESC);
`
