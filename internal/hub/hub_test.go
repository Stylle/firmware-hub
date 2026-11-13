package hub

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Stylle/firmware-hub/internal/protocol"
	"golang.org/x/crypto/bcrypt"
)

type fixture struct {
	a      *App
	t      *testing.T
	cookie *http.Cookie
	csrf   string
	key    ed25519.PrivateKey
	pid    int64
	token  string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	a, e := Open(t.TempDir(), "admin", "TestOnly-Password-1234")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close() })
	f := &fixture{a: a, t: t}
	r := f.call("POST", "/api/v1/session", map[string]any{"name": "admin", "password": "TestOnly-Password-1234"}, false, "")
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	f.cookie = r.Result().Cookies()[0]
	var v struct {
		User user `json:"user"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &v)
	f.csrf = v.User.CSRF
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	f.key = key
	r = f.call("POST", "/api/v1/projects", project{Slug: "demo", Name: "Demo", PublicKey: hex.EncodeToString(pub), Heartbeat: 15, Timeout: 30}, true, "")
	if r.Code != 201 {
		t.Fatal(r.Body.String())
	}
	var p project
	_ = json.Unmarshal(r.Body.Bytes(), &p)
	f.pid = p.ID
	r = f.call("POST", "/api/v1/projects/"+strconv.FormatInt(f.pid, 10)+"/devices", map[string]string{"name": "client"}, true, "")
	var d struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &d)
	f.token = d.Token
	return f
}
func (f *fixture) call(method, path string, v any, auth bool, token string) *httptest.ResponseRecorder {
	f.t.Helper()
	var body io.Reader
	if v != nil {
		b, e := json.Marshal(v)
		if e != nil {
			f.t.Fatal(e)
		}
		body = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, body)
	r.RemoteAddr = "127.0.0.1:12345"
	if auth {
		r.AddCookie(f.cookie)
		r.Header.Set("X-CSRF-Token", f.csrf)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.a.Handler(nil).ServeHTTP(w, r)
	return w
}
func (f *fixture) upload(data []byte, tamper bool) *httptest.ResponseRecorder {
	hash := sha256.Sum256(data)
	m := protocol.Manifest{Project: "demo", Version: "1.1.0", Sequence: 2, Security: 1, Channel: "stable", Platform: "test", Arch: "x64", Hardware: "demo", SHA256: hex.EncodeToString(hash[:]), Size: int64(len(data)), Expires: now() + 3600}
	b, _ := m.Bytes()
	env := protocol.Envelope{Payload: base64.StdEncoding.EncodeToString(b), Signature: hex.EncodeToString(ed25519.Sign(f.key, b))}
	envelope, _ := json.Marshal(env)
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("manifest", string(envelope))
	_ = writer.WriteField("notes", "<script>not executed</script> release notes")
	part, _ := writer.CreateFormFile("file", "../unsafe.bin")
	if tamper {
		data = append([]byte(nil), data...)
		data[0] ^= 1
	}
	_, _ = part.Write(data)
	_ = writer.Close()
	r := httptest.NewRequest("POST", "/api/v1/projects/"+strconv.FormatInt(f.pid, 10)+"/releases", &buf)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("X-CSRF-Token", f.csrf)
	r.AddCookie(f.cookie)
	w := httptest.NewRecorder()
	f.a.Handler(nil).ServeHTTP(w, r)
	return w
}
func (f *fixture) published(data []byte) int64 {
	r := f.upload(data, false)
	if r.Code != 201 {
		f.t.Fatal(r.Body.String())
	}
	var v struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &v)
	r = f.call("PUT", "/api/v1/releases/"+strconv.FormatInt(v.ID, 10)+"/publish", map[string]bool{"published": true}, true, "")
	if r.Code != 200 {
		f.t.Fatal(r.Body.String())
	}
	return v.ID
}

func TestOptInAndHeartbeatIdentity(t *testing.T) {
	f := setup(t)
	body := map[string]string{"version": "1.0.0", "platform": "test", "arch": "x64", "hardware": "demo", "status": "idle"}
	path := "/api/v1/update/demo/heartbeat"
	if r := f.call("POST", path, body, false, f.token); r.Code != 403 {
		t.Fatalf("default telemetry not disabled: %d", r.Code)
	}
	_, _ = f.a.DB.Exec("UPDATE projects SET telemetry=1 WHERE id=?", f.pid)
	if r := f.call("POST", path, body, false, f.token); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var version string
	var last int64
	_ = f.a.DB.QueryRow("SELECT version,last_seen FROM devices WHERE token_hash=?", digest(f.token)).Scan(&version, &last)
	if version != "1.0.0" || last == 0 {
		t.Fatal("version not saved")
	}
	if r := f.call("POST", path, body, false, f.token); r.Code != 429 {
		t.Fatal("heartbeat rate not limited")
	}
	_, _ = f.a.DB.Exec("UPDATE settings SET telemetry=0 WHERE id=1")
	if r := f.call("POST", path, body, false, f.token); r.Code != 403 {
		t.Fatal("global off ignored")
	}
	var after int64
	_ = f.a.DB.QueryRow("SELECT last_seen FROM devices WHERE token_hash=?", digest(f.token)).Scan(&after)
	if after != last {
		t.Fatal("disabled collection changed stored data")
	}
	r := f.call("GET", "/api/v1/projects/"+strconv.FormatInt(f.pid, 10)+"/devices", nil, true, "")
	if !strings.Contains(r.Body.String(), `"online_state":"disabled"`) {
		t.Fatal(r.Body.String())
	}
	_, _ = f.a.DB.Exec("UPDATE settings SET telemetry=1 WHERE id=1")
	_, _ = f.a.DB.Exec("UPDATE devices SET last_seen=?", now()-31)
	r = f.call("GET", "/api/v1/projects/"+strconv.FormatInt(f.pid, 10)+"/devices", nil, true, "")
	if !strings.Contains(r.Body.String(), `"online_state":"offline"`) {
		t.Fatal("timeout not reflected")
	}
	_, _ = f.a.DB.Exec("UPDATE devices SET revoked=1")
	if r := f.call("POST", path, body, false, f.token); r.Code != 401 {
		t.Fatal("revoked token accepted")
	}
}
func TestSignedDownloadRangeAndCompatibility(t *testing.T) {
	f := setup(t)
	data := []byte("a firmware artifact with stable immutable bytes")
	rid := f.published(data)
	path := "/api/v1/artifacts/" + strconv.FormatInt(rid, 10)
	if r := f.call("GET", path, nil, false, ""); r.Code != 401 {
		t.Fatal("private artifact exposed")
	}
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Range", "bytes=2-8")
	w := httptest.NewRecorder()
	f.a.Handler(nil).ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != string(data[2:9]) {
		t.Fatal(w.Code, w.Body.String())
	}
	etag := w.Header().Get("ETag")
	r = httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Range", "bytes=9999-")
	w = httptest.NewRecorder()
	f.a.Handler(nil).ServeHTTP(w, r)
	if w.Code != 416 {
		t.Fatal("invalid range not refused")
	}
	r = httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Range", "bytes=2-")
	r.Header.Set("If-Range", `"changed"`)
	w = httptest.NewRecorder()
	f.a.Handler(nil).ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("ETag") != etag || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal("If-Range mismatch should restart")
	}
	query := "/api/v1/update/demo/check?sequence=0&platform=test&arch=x64&hardware=demo"
	w = f.call("GET", query, nil, false, f.token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"update":true`) {
		t.Fatal(w.Body.String())
	}
	if len(w.Body.Bytes()) > 4096 {
		t.Fatal("MCU metadata not bounded")
	}
	w = f.call("GET", strings.Replace(query, "arch=x64", "arch=arm", 1), nil, false, f.token)
	if !strings.Contains(w.Body.String(), `"update":false`) {
		t.Fatal("wrong architecture offered")
	}
	w = f.call("GET", query+"&security=2", nil, false, f.token)
	if !strings.Contains(w.Body.String(), `"update":false`) {
		t.Fatal("security floor ignored")
	}
	f.call("PUT", "/api/v1/releases/"+strconv.FormatInt(rid, 10)+"/publish", map[string]bool{"published": false}, true, "")
	if w = f.call("GET", path, nil, false, f.token); w.Code != 401 {
		t.Fatal("withdrawn artifact exposed")
	}
}
func TestTamperAndImmutableRelease(t *testing.T) {
	f := setup(t)
	data := []byte("firmware")
	if w := f.upload(data, true); w.Code != 400 {
		t.Fatal("tampered artifact accepted")
	}
	if w := f.upload(data, false); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if w := f.upload(data, false); w.Code != 409 {
		t.Fatal("duplicate release overwritten")
	}
}
func TestAuthCSRFAndCrossProject(t *testing.T) {
	f := setup(t)
	w := f.call("GET", "/api/v1/projects", nil, false, "")
	if w.Code != 401 {
		t.Fatal("unauthenticated projects exposed")
	}
	r := httptest.NewRequest("PUT", "/api/v1/settings", strings.NewReader(`{"telemetry":false}`))
	r.AddCookie(f.cookie)
	w = httptest.NewRecorder()
	f.a.Handler(nil).ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF not rejected")
	}
	h, _ := bcrypt.GenerateFromPassword([]byte("Other-Test-Password-123"), bcrypt.MinCost)
	res, _ := f.a.DB.Exec("INSERT INTO users(name,password) VALUES('other',?)", string(h))
	uid, _ := res.LastInsertId()
	other := randomToken()
	_, _ = f.a.DB.Exec("INSERT INTO sessions VALUES(?,?,?,?)", digest(other), uid, "csrf", now()+3600)
	f.cookie = &http.Cookie{Name: "fh_session", Value: other}
	f.csrf = "csrf"
	path := "/api/v1/projects/" + strconv.FormatInt(f.pid, 10)
	for _, suffix := range []string{"/devices", "/releases", "/members", "/logs", "/notifications"} {
		if w = f.call("GET", path+suffix, nil, true, ""); w.Code != 403 {
			t.Fatal("cross-project access", suffix, w.Code)
		}
	}
	w = f.call("GET", "/api/v1/projects", nil, true, "")
	if w.Body.String() != "[]\n" {
		t.Fatal("project list not isolated")
	}
}
func TestWebhookOutboxAndSecretStorage(t *testing.T) {
	f := setup(t)
	var sent atomic.Int32
	var valid atomic.Bool
	secret := "Test-Webhook-Secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(r.Header.Get("X-FH-Timestamp") + "." + r.Header.Get("X-FH-Event") + "."))
		mac.Write(body)
		valid.Store(hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(r.Header.Get("X-FH-Signature"))))
		sent.Add(1)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	path := "/api/v1/projects/" + strconv.FormatInt(f.pid, 10) + "/notifications"
	w := f.call("PUT", path, notification{Webhook: srv.URL, WebhookSecret: secret, AllowPrivate: true}, true, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	n, _ := f.a.notification(f.pid)
	if n.WebhookSecret == secret || n.WebhookSecret == "" {
		t.Fatal("secret stored plaintext")
	}
	w = f.call("GET", path, nil, true, "")
	if strings.Contains(w.Body.String(), secret) {
		t.Fatal("secret leaked in GET")
	}
	f.published([]byte("test image"))
	f.a.deliverOne(context.Background())
	if sent.Load() != 1 || !valid.Load() {
		t.Fatal("webhook signature/delivery failed")
	}
	var state string
	_ = f.a.DB.QueryRow("SELECT state FROM outbox LIMIT 1").Scan(&state)
	if state != "delivered" {
		t.Fatal(state)
	}
}
func TestSSRFAndRedirect(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "169.254.169.254", "192.168.1.1", "0.0.0.0"} {
		if _, e := safeIPs(context.Background(), host, false); e == nil {
			t.Fatal("unsafe target allowed", host)
		}
	}
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	req, _ := http.NewRequest("POST", source.URL, nil)
	if postResult(safeHTTP(true), req) == nil || reached.Load() {
		t.Fatal("redirect followed")
	}
}
func TestJSONTrailingData(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"a":1} garbage`))
	w := httptest.NewRecorder()
	var v struct {
		A int `json:"a"`
	}
	if readJSON(w, r, &v) == nil {
		t.Fatal("trailing garbage accepted")
	}
}
func TestPublicHasNoDeviceData(t *testing.T) {
	f := setup(t)
	_, _ = f.a.DB.Exec("UPDATE projects SET public=1 WHERE id=?", f.pid)
	f.published([]byte("public artifact"))
	w := f.call("GET", "/api/v1/public/demo", nil, false, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), f.token) || strings.Contains(w.Body.String(), "token_hash") || strings.Contains(w.Body.String(), "last_seen") {
		t.Fatal("public data leaked")
	}
}
func TestNotificationTLSValidation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer server.Close()
	r, _ := http.NewRequest("POST", server.URL, nil)
	if postResult(safeHTTP(true), r) == nil {
		t.Fatal("untrusted TLS accepted")
	}
	_, e := url.Parse(server.URL)
	if e != nil {
		t.Fatal(e)
	}
}

func TestBackupRestore(t *testing.T) {
	f := setup(t)
	f.published([]byte("immutable firmware"))
	_, _ = f.a.DB.Exec("INSERT INTO notifications(project_id,webhook_secret) VALUES(?,?)", f.pid, f.a.seal("backup-test-secret"))
	out := filepath.Join(t.TempDir(), "backup.zip")
	if e := f.a.Backup(out); e != nil {
		t.Fatal(e)
	}
	z, e := zip.OpenReader(out)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	dir := t.TempDir()
	for _, entry := range z.File {
		if strings.Contains(entry.Name, "..") || filepath.IsAbs(entry.Name) {
			t.Fatal("unsafe backup path")
		}
		target := filepath.Join(dir, filepath.FromSlash(entry.Name))
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			t.Fatal(e)
		}
		r, e := entry.Open()
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(target, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	restored, e := Open(dir, "", "")
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	n, e := restored.notification(f.pid)
	if e != nil {
		t.Fatal(e)
	}
	plain, e := restored.unseal(n.WebhookSecret)
	if e != nil || plain != "backup-test-secret" {
		t.Fatal("matching master key/configuration not restored")
	}
	rs, e := restored.listReleases(f.pid, true)
	if e != nil || len(rs) != 1 {
		t.Fatal("release not restored")
	}
	if _, e = os.Stat(filepath.Join(dir, "artifacts", rs[0].SHA256)); e != nil {
		t.Fatal(e)
	}
	if e = f.a.Backup(out); e == nil {
		t.Fatal("existing backup overwritten")
	}
}
