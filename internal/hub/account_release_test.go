package hub

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func (f *fixture) accountUpload(pid int64, meta releaseMetadata, contents ...[]byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	b, _ := json.Marshal(meta)
	_ = mw.WriteField("metadata", string(b))
	_ = mw.WriteField("notes", "## Test release")
	part, _ := mw.CreateFormFile("file", "module.dll")
	data := []byte("inert-test-not-a-dll")
	if len(contents) > 0 {
		data = contents[0]
	}
	_, _ = part.Write(data)
	_ = mw.Close()
	r := httptest.NewRequest("POST", "/api/v1/projects/"+strconv.FormatInt(pid, 10)+"/releases", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("X-CSRF-Token", f.csrf)
	r.AddCookie(f.cookie)
	w := httptest.NewRecorder()
	f.a.Handler(nil).ServeHTTP(w, r)
	return w
}

func TestRejectedUploadsDoNotRetainArtifacts(t *testing.T) {
	f := setup(t)
	r := f.call("POST", "/api/v1/projects", project{Slug: "account-fixture", Name: "Fixture", Heartbeat: 60, Timeout: 180}, true, "")
	if r.Code != 201 {
		t.Fatal("project failed")
	}
	var p project
	_ = json.Unmarshal(r.Body.Bytes(), &p)
	meta := releaseMetadata{Version: "1.0.0", Channel: "stable", Platform: "any", Arch: "any", Hardware: "any", PackageKind: "full"}
	if f.accountUpload(p.ID, meta).Code != 201 {
		t.Fatal("initial upload failed")
	}
	for _, payload := range []string{"different-one", "different-two", "inert-test-not-a-dll"} {
		if f.accountUpload(p.ID, meta, []byte(payload)).Code != 409 {
			t.Fatal("duplicate accepted")
		}
	}
	entries, e := os.ReadDir(filepath.Join(f.a.Data, "artifacts"))
	if e != nil || len(entries) != 1 {
		t.Fatal("failed uploads retained files")
	}
	_, e = f.a.DB.Exec("CREATE TRIGGER fixture_failure BEFORE INSERT ON release_info BEGIN SELECT RAISE(ABORT,'fixture'); END")
	if e != nil {
		t.Fatal(e)
	}
	meta.Version = "2.0.0"
	if f.accountUpload(p.ID, meta, []byte("failed-metadata")).Code != 500 {
		t.Fatal("metadata failure not triggered")
	}
	entries, e = os.ReadDir(filepath.Join(f.a.Data, "artifacts"))
	if e != nil || len(entries) != 1 {
		t.Fatal("metadata failure retained files")
	}
	var count int
	_ = f.a.DB.QueryRow("SELECT COUNT(*) FROM releases WHERE project_id=?", p.ID).Scan(&count)
	if count != 1 {
		t.Fatal("metadata rollback failed")
	}
}
func TestAccountReleasesAndPublicDiscovery(t *testing.T) {
	f := setup(t)
	password := "Publish-Test-Only-123"
	p := project{Slug: "desktop", Name: "Desktop", Public: true, Heartbeat: 60, Timeout: 180, PublishPassword: &password}
	r := f.call("POST", "/api/v1/projects", p, true, "")
	if r.Code != 201 {
		t.Fatal(r.Body.String())
	}
	_ = json.Unmarshal(r.Body.Bytes(), &p)
	if p.ID == 0 || !p.HasPublishPassword || strings.Contains(r.Body.String(), password) {
		t.Fatal("password leaked or project failed")
	}
	meta := releaseMetadata{Version: "1.0.0", Channel: "stable", Platform: "any", Arch: "any", Hardware: "any", PackageKind: "full"}
	if r = f.accountUpload(f.pid, meta); r.Code != 400 {
		t.Fatal("unsigned upload accepted in signed project")
	}
	upload := func() int64 {
		r := f.accountUpload(p.ID, meta)
		if r.Code != 201 {
			t.Fatal(r.Body.String())
		}
		var v struct{ ID int64 }
		_ = json.Unmarshal(r.Body.Bytes(), &v)
		return v.ID
	}
	publish := func(id int64) {
		path := "/api/v1/releases/" + strconv.FormatInt(id, 10) + "/publish"
		if r := f.call("PUT", path, map[string]any{"published": true}, true, ""); r.Code != 403 {
			t.Fatal("missing publish password accepted", r.Code)
		}
		if r := f.call("PUT", path, map[string]any{"published": true, "publish_password": password}, true, ""); r.Code != 200 {
			t.Fatal(r.Body.String())
		}
	}
	first := upload()
	publish(first)
	meta.Version = "1.1.0"
	meta.PackageKind = "file"
	meta.BaseVersion = "1.0.0"
	meta.TargetPath = "plugins/module.dll"
	second := upload()
	publish(second)
	r = f.call("GET", "/api/v1/public", nil, false, "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "desktop") || strings.Contains(r.Body.String(), `"demo"`) {
		t.Fatal("public discovery leaked private project", r.Body.String())
	}
	r = f.call("GET", "/api/v1/public/desktop", nil, false, "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"published_by":"admin"`) || !strings.Contains(r.Body.String(), `"uploaded_by":"admin"`) || strings.Contains(r.Body.String(), password) {
		t.Fatal(r.Body.String())
	}
	for _, v := range []struct{ version, want string }{{"1.0.0", "1.1.0"}, {"0.9.0", "1.0.0"}} {
		r = f.call("GET", "/api/v1/update/desktop/check?version="+v.version, nil, false, "")
		if r.Code != 200 || !strings.Contains(r.Body.String(), `"version":"`+v.want+`"`) {
			t.Fatalf("compatibility %s: %s", v.version, r.Body.String())
		}
	}
	r = f.call("GET", "/api/v1/artifacts/"+strconv.FormatInt(second, 10), nil, false, "")
	if r.Code != 200 || !strings.Contains(r.Header().Get("Content-Disposition"), "module.dll") {
		t.Fatal("original filename lost")
	}
	r = f.call("GET", "/api/v1/projects/"+strconv.FormatInt(p.ID, 10)+"/audit", nil, true, "")
	if !strings.Contains(r.Body.String(), "release.publish id=") {
		t.Fatal("publish provenance missing")
	}
	var stored string
	_ = f.a.DB.QueryRow("SELECT publish_password FROM project_security WHERE project_id=?", p.ID).Scan(&stored)
	if stored == password || !strings.HasPrefix(stored, "$2") {
		t.Fatal("publish password not hashed")
	}
}
func TestSecurityModeChangeRequiresAccountPassword(t *testing.T) {
	f := setup(t)
	p, _ := f.a.project(f.pid)
	p.PublicKey = ""
	path := "/api/v1/projects/" + strconv.FormatInt(p.ID, 10)
	if r := f.call("PUT", path, p, true, ""); r.Code != 403 {
		t.Fatal("signature downgrade without reauth")
	}
	p.AccountPassword = "TestOnly-Password-1234"
	if r := f.call("PUT", path, p, true, ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var key string
	_ = f.a.DB.QueryRow("SELECT pubkey FROM projects WHERE id=?", p.ID).Scan(&key)
	if key != "" {
		t.Fatal("account mode not saved")
	}
}
func TestLoginActivityAndSessionRevocation(t *testing.T) {
	f := setup(t)
	if r := f.call("POST", "/api/v1/session", map[string]string{"name": "admin", "password": "wrong"}, false, ""); r.Code != 401 {
		t.Fatal("bad login accepted")
	}
	r := f.call("GET", "/api/v1/activity?all=1", nil, true, "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "invalid_credentials") || !strings.Contains(r.Body.String(), `"current":true`) || strings.Contains(r.Body.String(), digest(f.cookie.Value)) {
		t.Fatal(r.Body.String())
	}
	var out struct{ Sessions []struct{ ID string } }
	_ = json.Unmarshal(r.Body.Bytes(), &out)
	if len(out.Sessions) != 1 {
		t.Fatal("session info missing")
	}
	r = f.call("DELETE", "/api/v1/sessions/"+out.Sessions[0].ID, nil, true, "")
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r = f.call("GET", "/api/v1/projects", nil, true, ""); r.Code != 401 {
		t.Fatal("revoked session active")
	}
	var count int
	_ = f.a.DB.QueryRow("SELECT count(*) FROM session_info").Scan(&count)
	if count != 0 {
		t.Fatal("orphan session info")
	}
}
func TestPortablePackagePaths(t *testing.T) {
	for _, s := range []string{"../a", "/a", "C:/a", "a\\b", "a/../b", "a/", "a//b", "a.", "a ", "CON.txt", "plugins/LPT1.dll", "a:stream", "a\x00", "a\n", "NUL", "COM0"} {
		if validPackagePath(s) {
			t.Errorf("unsafe path accepted %q", s)
		}
	}
	for _, s := range []string{"App.exe", "plugins/module.dll", "translations/app_zh.qm", "软件.bin"} {
		if !validPackagePath(s) {
			t.Errorf("valid path refused %q", s)
		}
	}
}
