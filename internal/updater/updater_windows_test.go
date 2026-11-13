package updater

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWindowsLockedFileRollsBackEarlierReplacements(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.dll", "z.exe"} {
		if e := os.WriteFile(filepath.Join(dir, name), []byte("original"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	name, e := syscall.UTF16PtrFromString(filepath.Join(dir, "z.exe"))
	if e != nil {
		t.Fatal(e)
	}
	handle, e := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer syscall.CloseHandle(handle)
	p := plan(t, dir, zipData(t, map[string]string{"a.dll": "new dll", "z.exe": "new exe"}), "full")
	if e = Apply(p); e == nil {
		t.Fatal("locked application replaced")
	}
	for _, file := range []string{"a.dll", "z.exe"} {
		if b, e := os.ReadFile(filepath.Join(dir, file)); e != nil || string(b) != "original" {
			t.Fatal("partial install not recovered", file, e)
		}
	}
}
