package hub

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Stylle/firmware-hub/internal/portablebackup"
)

func TestPortableRestoreIntoNewDeployment(t *testing.T) {
	f := setup(t)
	if _, e := f.a.DB.Exec("INSERT INTO notifications(project_id,webhook_secret) VALUES(?,?)", f.pid, f.a.seal("portable-secret")); e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if e := portablebackup.Export(f.a.Data, f.a.DB, backupSpec, archive); e != nil {
		t.Fatal(e)
	}
	targetDir := t.TempDir()
	target, e := Open(targetDir, "differentadmin", "Different-Password-1234")
	if e != nil {
		t.Fatal(e)
	}
	target.Close()
	if e = portablebackup.Stage(targetDir, archive, backupSpec, validateHubBackup); e != nil {
		t.Fatal(e)
	}
	restored, e := Open(targetDir, "differentadmin", "Different-Password-1234")
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	var projects, admins, sessions int
	restored.DB.QueryRow("SELECT COUNT(*) FROM projects").Scan(&projects)
	restored.DB.QueryRow("SELECT COUNT(*) FROM users WHERE name='admin'").Scan(&admins)
	restored.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions)
	if projects != 1 || admins != 1 || sessions != 0 {
		t.Fatal("business data/account/session restore mismatch")
	}
	notice, e := restored.notification(f.pid)
	if e != nil {
		t.Fatal(e)
	}
	if value, e := restored.unseal(notice.WebhookSecret); e != nil || value != "portable-secret" {
		t.Fatal("notification encryption key not restored")
	}
	previous, _ := filepath.Glob(filepath.Join(targetDir, "previous-*", "hub.sqlite"))
	if len(previous) != 1 {
		t.Fatal("previous database must be retained")
	}
	if _, e = os.Stat(filepath.Join(targetDir, "restore-ready.json")); !os.IsNotExist(e) {
		t.Fatal("restore plan was not completed")
	}
}
