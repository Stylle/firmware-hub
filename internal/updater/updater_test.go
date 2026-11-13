package updater

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func plan(t *testing.T, dir string, data []byte, kind string) Plan {
	t.Helper()
	source := filepath.Join(t.TempDir(), "package.bin")
	if e := os.WriteFile(source, data, 0600); e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(data)
	return Plan{Source: source, InstallDir: dir, SHA256: hex.EncodeToString(h[:]), Size: int64(len(data)), Project: "desktop", Version: "1.1.0", Sequence: 2, CurrentVersion: "1.0.0", PackageKind: kind, BaseVersion: "1.0.0", TargetPath: "plugins/test.dll"}
}
func zipData(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, content := range files {
		f, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = f.Write([]byte(content))
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestSingleFileAndRollback(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "plugins"), 0700)
	_ = os.WriteFile(filepath.Join(dir, "plugins/test.dll"), []byte("old"), 0600)
	p := plan(t, dir, []byte("new"), "file")
	if e := Apply(p); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "plugins/test.dll")); string(b) != "new" {
		t.Fatal("not replaced")
	}
	if e := Apply(p); e == nil {
		t.Fatal("rollback sequence accepted")
	}
	if e := Rollback(dir); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "plugins/test.dll")); string(b) != "old" {
		t.Fatal("not restored")
	}
	if _, e := os.Stat(filepath.Join(dir, versionFile)); !os.IsNotExist(e) {
		t.Fatal("version state not restored")
	}
}
func TestFullPackagePreservesUserDataAndRetainsBackups(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "settings.ini"), []byte("user settings"), 0600)
	p := plan(t, dir, zipData(t, map[string]string{"App.exe": "inert executable", "plugins/a.dll": "dll", "empty.txt": ""}), "full")
	if e := Apply(p); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "settings.ini")); string(b) != "user settings" {
		t.Fatal("user data changed")
	}
	p.Version = "1.2.0"
	p.Sequence = 3
	if e := Apply(p); e != nil {
		t.Fatal(e)
	}
	dirs, _ := filepath.Glob(filepath.Join(dir, ".firmware-hub-backup-*"))
	if len(dirs) != 1 {
		t.Fatal("previous backup not retained")
	}
}
func TestRejectUnsafePackagesBeforeTouchingApp(t *testing.T) {
	for _, name := range []string{"../escape", "C:/escape", "a\\escape", "plugins/NUL.txt", ".firmware-hub-version.json", "a:stream"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := plan(t, dir, zipData(t, map[string]string{name: "bad", "safe.txt": "safe"}), "full")
			if e := Apply(p); e == nil {
				t.Fatal("unsafe zip accepted")
			}
			if _, e := os.Stat(filepath.Join(dir, "safe.txt")); !os.IsNotExist(e) {
				t.Fatal("files changed before validation")
			}
		})
	}
	dir := t.TempDir()
	p := plan(t, dir, zipData(t, map[string]string{"App.exe": "a", "app.EXE": "b"}), "full")
	if e := Apply(p); e == nil {
		t.Fatal("case collision accepted")
	}
	p = plan(t, dir, []byte("file"), "file")
	p.CurrentVersion = "0.1.0"
	if e := Apply(p); e == nil {
		t.Fatal("wrong base accepted")
	}
	p.CurrentVersion = p.BaseVersion
	p.SHA256 = strings.Repeat("0", 64)
	if e := Apply(p); e == nil {
		t.Fatal("bad hash accepted")
	}
}
func TestFailedInstallRestoresAndChangedFilesBlockRollback(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "App.exe"), []byte("old"), 0600)
	p := plan(t, dir, zipData(t, map[string]string{"App.exe": "new"}), "full")
	if e := Apply(p); e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(filepath.Join(dir, "App.exe"), []byte("edited after update"), 0600)
	if e := Rollback(dir); e == nil {
		t.Fatal("rollback overwrote later local changes")
	}
}
func TestLinksCannotEscapeInstallDirectory(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if e := os.Symlink(outside, filepath.Join(dir, "plugins")); e != nil {
		t.Skip("symlink creation unavailable")
	}
	p := plan(t, dir, []byte("new"), "file")
	if e := Apply(p); e == nil {
		t.Fatal("symlink destination accepted")
	}
	if _, e := os.Stat(filepath.Join(outside, "test.dll")); !os.IsNotExist(e) {
		t.Fatal("outside file changed")
	}
}
