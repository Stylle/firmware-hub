// Package portablebackup implements staged, restart-time portable restores.
package portablebackup

import (
	"archive/zip"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const Limit int64 = 20 << 30

type Spec struct {
	App, Database, Key  string
	Directories, Tables []string
}
type entry struct {
	Size   int64
	SHA256 string
}
type manifest struct {
	App    string
	Format int
	Files  map[string]entry
}
type plan struct {
	Stage, Previous string
	Names           []string
}

func token() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func names(s Spec) []string { return append([]string{s.Database, s.Key}, s.Directories...) }
func allowed(n string, s Spec) bool {
	if n == s.Database || n == s.Key {
		return true
	}
	if strings.ContainsAny(n, "\\:\x00") || strings.HasPrefix(n, "/") {
		return false
	}
	for _, part := range strings.Split(n, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for _, d := range s.Directories {
		if strings.HasPrefix(n, d+"/") {
			return true
		}
	}
	return false
}

// Export snapshots SQLite and packages application-owned files plus its encryption key.
func Export(dir string, db *sql.DB, s Spec, destination string) error {
	stage, e := os.MkdirTemp(dir, ".export-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	snapshot := filepath.Join(stage, s.Database)
	if _, e = db.Exec("VACUUM INTO '" + strings.ReplaceAll(filepath.ToSlash(snapshot), "'", "''") + "'"); e != nil {
		return e
	}
	copyDB, e := sql.Open("sqlite", snapshot)
	if e != nil {
		return e
	}
	_, e = copyDB.Exec("DELETE FROM sessions")
	closeErr := copyDB.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	out, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer out.Close()
	z := zip.NewWriter(out)
	defer z.Close()
	m := manifest{App: s.App, Format: 1, Files: map[string]entry{}}
	var total int64
	add := func(source, n string) error {
		st, e := os.Lstat(source)
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() {
			return errors.New("nonregular backup file")
		}
		total += st.Size()
		if total > Limit {
			return errors.New("backup exceeds 20 GB")
		}
		f, e := os.Open(source)
		if e != nil {
			return e
		}
		defer f.Close()
		h := &zip.FileHeader{Name: n, Method: zip.Store}
		h.SetMode(0600)
		w, e := z.CreateHeader(h)
		if e != nil {
			return e
		}
		hash := sha256.New()
		count, e := io.Copy(io.MultiWriter(w, hash), f)
		if e != nil {
			return e
		}
		m.Files[n] = entry{count, hex.EncodeToString(hash.Sum(nil))}
		return nil
	}
	if e = add(snapshot, s.Database); e != nil {
		return e
	}
	if e = add(filepath.Join(dir, s.Key), s.Key); e != nil {
		return e
	}
	for _, d := range s.Directories {
		root := filepath.Join(dir, d)
		if _, e = os.Stat(root); errors.Is(e, os.ErrNotExist) {
			continue
		}
		e = filepath.WalkDir(root, func(p string, item os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if item.IsDir() {
				return nil
			}
			rel, e := filepath.Rel(dir, p)
			if e != nil {
				return e
			}
			return add(p, filepath.ToSlash(rel))
		})
		if e != nil {
			return e
		}
	}
	w, e := z.Create("MANIFEST.json")
	if e != nil {
		return e
	}
	if e = json.NewEncoder(w).Encode(m); e != nil {
		return e
	}
	if e = z.Close(); e != nil {
		return e
	}
	return out.Sync()
}

// Stage validates all content before writing a durable restore plan. Live data is untouched.
func Stage(dir, archive string, s Spec, validate func(string) error) error {
	if _, e := os.Stat(filepath.Join(dir, "restore-ready.json")); e == nil {
		return errors.New("restore already pending")
	}
	z, e := zip.OpenReader(archive)
	if e != nil {
		return e
	}
	defer z.Close()
	if len(z.File) > 1000000 {
		return errors.New("too many backup files")
	}
	stage, e := os.MkdirTemp(dir, "restore-")
	if e != nil {
		return e
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(stage)
		}
	}()
	seen := map[string]bool{}
	var total uint64
	var m manifest
	for _, f := range z.File {
		n := f.Name
		if seen[n] || !f.Mode().IsRegular() || (n != "MANIFEST.json" && !allowed(n, s)) {
			return errors.New("invalid archive entry")
		}
		seen[n] = true
		if f.UncompressedSize64 > uint64(Limit) {
			return errors.New("entry too large")
		}
		total += f.UncompressedSize64
		if total > uint64(Limit) {
			return errors.New("backup exceeds 20 GB")
		}
		in, e := f.Open()
		if e != nil {
			return e
		}
		if n == "MANIFEST.json" {
			e = json.NewDecoder(io.LimitReader(in, 8<<20)).Decode(&m)
			in.Close()
			if e != nil {
				return e
			}
			continue
		}
		target := filepath.Join(stage, filepath.FromSlash(n))
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			in.Close()
			return e
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			return e
		}
		_, e = io.Copy(out, io.LimitReader(in, int64(f.UncompressedSize64)+1))
		ce := out.Close()
		in.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	if m.App != s.App || m.Format != 1 || !seen[s.Database] || !seen[s.Key] || len(m.Files) != len(seen)-1 {
		return errors.New("incomplete or wrong application backup")
	}
	for n, expected := range m.Files {
		if !allowed(n, s) || !seen[n] {
			return errors.New("invalid manifest")
		}
		f, e := os.Open(filepath.Join(stage, filepath.FromSlash(n)))
		if e != nil {
			return e
		}
		h := sha256.New()
		size, e := io.Copy(h, f)
		f.Close()
		if e != nil || size != expected.Size || hex.EncodeToString(h.Sum(nil)) != expected.SHA256 {
			return errors.New("backup checksum mismatch")
		}
	}
	key, e := os.ReadFile(filepath.Join(stage, s.Key))
	if e != nil || len(key) != 32 {
		return errors.New("invalid encryption key")
	}
	db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(stage, s.Database))+"?mode=ro")
	if e != nil {
		return e
	}
	var integrity string
	e = db.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	if e == nil && integrity != "ok" {
		e = errors.New("database integrity failure")
	}
	for _, table := range s.Tables {
		if e != nil {
			break
		}
		var count int
		e = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if e == nil && count != 1 {
			e = errors.New("missing application table")
		}
	}
	db.Close()
	if e != nil {
		return e
	}
	if validate != nil {
		if e = validate(stage); e != nil {
			return e
		}
	}
	// Imported browser sessions must never be replayed.
	clean, e := sql.Open("sqlite", filepath.Join(stage, s.Database))
	if e != nil {
		return e
	}
	_, e = clean.Exec("DELETE FROM sessions")
	ce := clean.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	for _, d := range s.Directories {
		if e = os.MkdirAll(filepath.Join(stage, d), 0700); e != nil {
			return e
		}
	}
	p := plan{Stage: filepath.Base(stage), Previous: "previous-" + token(), Names: names(s)}
	b, _ := json.Marshal(p)
	temp := filepath.Join(dir, ".restore-plan-"+token())
	if e = os.WriteFile(temp, b, 0600); e != nil {
		return e
	}
	if e = os.Rename(temp, filepath.Join(dir, "restore-ready.json")); e != nil {
		os.Remove(temp)
		return e
	}
	keep = true
	return nil
}

// Apply runs before opening SQLite. Moves are restartable; old data is retained.
func Apply(dir string, s Spec) error {
	marker := filepath.Join(dir, "restore-ready.json")
	b, e := os.ReadFile(marker)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var p plan
	if json.Unmarshal(b, &p) != nil || !strings.HasPrefix(p.Stage, "restore-") || filepath.Base(p.Stage) != p.Stage || !strings.HasPrefix(p.Previous, "previous-") || filepath.Base(p.Previous) != p.Previous {
		return errors.New("invalid restore plan")
	}
	if strings.Join(p.Names, "|") != strings.Join(names(s), "|") {
		return errors.New("wrong restore application")
	}
	old := filepath.Join(dir, p.Previous)
	if e = os.MkdirAll(old, 0700); e != nil {
		return e
	}
	for _, n := range append([]string{s.Database + "-wal", s.Database + "-shm"}, p.Names...) {
		current := filepath.Join(dir, n)
		saved := filepath.Join(old, n)
		incoming := filepath.Join(dir, p.Stage, n)
		sidecar := n == s.Database+"-wal" || n == s.Database+"-shm"
		if !sidecar {
			if _, e = os.Stat(incoming); errors.Is(e, os.ErrNotExist) {
				continue
			} else if e != nil {
				return e
			}
		}
		if _, e = os.Stat(saved); errors.Is(e, os.ErrNotExist) {
			if e = os.Rename(current, saved); e != nil && !errors.Is(e, os.ErrNotExist) {
				return fmt.Errorf("retain old data: %w", e)
			}
		}
		if !sidecar {
			if e = os.Rename(incoming, current); e != nil {
				return e
			}
		}
	}
	if e = os.Remove(marker); e != nil {
		return e
	}
	_ = os.Remove(filepath.Join(dir, p.Stage))
	return nil
}
