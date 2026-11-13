package portablebackup

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"testing"
)

func TestRejectUnsafeAndWrongArchivesWithoutChangingTarget(t *testing.T) {
	spec := Spec{App: "fixture", Database: "app.db", Key: "secret.key", Tables: []string{"users", "sessions"}}
	dir := t.TempDir()
	original := []byte("existing data")
	os.WriteFile(filepath.Join(dir, "app.db"), original, 0600)
	for _, name := range []string{"../escape", "C:/escape", "secret.key", "MANIFEST.json"} {
		path := filepath.Join(t.TempDir(), "bad.zip")
		f, _ := os.Create(path)
		z := zip.NewWriter(f)
		w, _ := z.Create(name)
		w.Write([]byte("invalid"))
		z.Close()
		f.Close()
		if Stage(dir, path, spec, nil) == nil {
			t.Fatal("invalid archive accepted")
		}
		current, _ := os.ReadFile(filepath.Join(dir, "app.db"))
		if string(current) != string(original) {
			t.Fatal("live data modified during failed validation")
		}
		if _, e := os.Stat(filepath.Join(dir, "restore-ready.json")); !os.IsNotExist(e) {
			t.Fatal("invalid backup created a restore plan")
		}
	}
}

func TestSnapshotAndInterruptedInstallationResume(t *testing.T) {
	spec := Spec{App: "fixture", Database: "app.db", Key: "secret.key", Directories: []string{"files"}, Tables: []string{"users", "sessions"}}
	source := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(source, "app.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec("CREATE TABLE users(name TEXT);INSERT INTO users VALUES('original');CREATE TABLE sessions(token TEXT);INSERT INTO sessions VALUES('old-session')")
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(source, "secret.key"), make([]byte, 32), 0600)
	os.Mkdir(filepath.Join(source, "files"), 0700)
	os.WriteFile(filepath.Join(source, "files", "data"), []byte("attachment"), 0600)
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if e = Export(source, db, spec, archive); e != nil {
		t.Fatal(e)
	}
	target := t.TempDir()
	os.WriteFile(filepath.Join(target, "app.db"), []byte("previous"), 0600)
	if e = Stage(target, archive, spec, nil); e != nil {
		t.Fatal(e)
	}
	// Simulate a crash after retaining the previous DB but before moving in the new DB.
	b, _ := os.ReadFile(filepath.Join(target, "restore-ready.json"))
	var p plan
	json.Unmarshal(b, &p)
	os.Mkdir(filepath.Join(target, p.Previous), 0700)
	os.Rename(filepath.Join(target, "app.db"), filepath.Join(target, p.Previous, "app.db"))
	if e = Apply(target, spec); e != nil {
		t.Fatal(e)
	}
	if e = Apply(target, spec); e != nil {
		t.Fatal(e)
	}
	restored, e := sql.Open("sqlite", filepath.Join(target, "app.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	var name string
	var sessions int
	restored.QueryRow("SELECT name FROM users").Scan(&name)
	restored.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions)
	if name != "original" || sessions != 0 {
		t.Fatal("data/session restoration mismatch")
	}
	if data, _ := os.ReadFile(filepath.Join(target, "files", "data")); string(data) != "attachment" {
		t.Fatal("attachment missing")
	}
}
