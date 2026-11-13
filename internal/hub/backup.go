package hub

import (
	"archive/zip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Backup creates a consistent SQLite snapshot plus immutable artifacts.
// It is a local administrator operation, not an HTTP endpoint. The archive
// includes master.key and must be encrypted/protected by the operator.
func (a *App) Backup(destination string) error {
	temp, e := os.MkdirTemp("", "firmware-hub-snapshot-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	snapshot := filepath.Join(temp, "hub.sqlite")
	if _, e = a.DB.Exec("VACUUM INTO '" + strings.ReplaceAll(filepath.ToSlash(snapshot), "'", "''") + "'"); e != nil {
		return e
	}
	snap, e := sql.Open("sqlite", snapshot)
	if e != nil {
		return e
	}
	defer snap.Close()
	f, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	complete := false
	defer func() {
		f.Close()
		if !complete {
			os.Remove(destination)
		}
	}()
	zw := zip.NewWriter(f)
	add := func(source, name string) error {
		st, e := os.Lstat(source)
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("refusing nonregular backup file")
		}
		in, e := os.Open(source)
		if e != nil {
			return e
		}
		defer in.Close()
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		h.SetMode(0600)
		out, e := zw.CreateHeader(h)
		if e != nil {
			return e
		}
		_, e = io.Copy(out, in)
		return e
	}
	if e = add(snapshot, "hub.sqlite"); e != nil {
		return e
	}
	if e = add(filepath.Join(a.Data, "master.key"), "master.key"); e != nil {
		return e
	}
	rows, e := snap.Query("SELECT DISTINCT sha256 FROM releases")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		if e = rows.Scan(&hash); e != nil {
			return e
		}
		if len(hash) != 64 || strings.ContainsAny(hash, "/\\.") {
			return fmt.Errorf("invalid stored artifact path")
		}
		if e = add(filepath.Join(a.Data, "artifacts", hash), "artifacts/"+hash); e != nil {
			return e
		}
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if e = zw.Close(); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	complete = true
	return nil
}
