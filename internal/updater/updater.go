// Package updater installs explicitly approved desktop packages into a local
// application directory. It never fetches URLs, executes package scripts, kills
// processes, elevates privileges, or guesses the application install location.
package updater

import (
	"archive/zip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Stylle/firmware-hub/internal/updatepath"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const work = ".firmware-hub-update"
const versionFile = ".firmware-hub-version.json"
const maxExpanded = int64(4 << 30)

// Plan is created by the local client after download verification. InstallDir
// and CurrentVersion are local choices, never supplied by a remote command.
// The checksum is an integrity check, not independent publisher authentication.
type Plan struct {
	Source         string `json:"source"`
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
	InstallDir     string `json:"install_dir"`
	Project        string `json:"project"`
	Version        string `json:"version"`
	Sequence       int64  `json:"sequence"`
	CurrentVersion string `json:"current_version"`
	PackageKind    string `json:"package_kind"`
	BaseVersion    string `json:"base_version"`
	TargetPath     string `json:"target_path"`
}
type installed struct {
	Project  string `json:"project"`
	Version  string `json:"version"`
	Sequence int64  `json:"sequence"`
}
type entry struct {
	Path    string
	NewHash string
	OldHash string
	Had     bool
}
type journal struct {
	Entries   []entry
	Committed bool
}

func open(dir string) (*os.Root, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) == filepath.VolumeName(dir)+string(filepath.Separator) {
		return nil, errors.New("choose a dedicated absolute application directory, not a drive root")
	}
	actual, e := filepath.EvalSymlinks(dir)
	if e != nil {
		return nil, e
	}
	if !strings.EqualFold(filepath.Clean(actual), filepath.Clean(dir)) {
		return nil, errors.New("install directory must not traverse symlinks/junctions")
	}
	return os.OpenRoot(dir)
}
func hashFile(f *os.File) (string, int64, error) {
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func fileHash(r *os.Root, name string) (string, error) {
	f, e := r.Open(name)
	if e != nil {
		return "", e
	}
	defer f.Close()
	s, e := f.Stat()
	if e != nil || !s.Mode().IsRegular() {
		return "", errors.New("not a regular file")
	}
	h, _, e := hashFile(f)
	return h, e
}
func save(r *os.Root, name string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	temp := name + ".tmp-" + rand.Text()
	f, e := r.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = r.Rename(temp, name)
	}
	if e != nil {
		_ = r.Remove(temp)
	}
	return e
}
func loadJournal(r *os.Root) (journal, error) {
	var j journal
	f, e := r.Open(work + "/journal.json")
	if e != nil {
		return j, e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 4<<20))
	d.DisallowUnknownFields()
	if e = d.Decode(&j); e != nil {
		return j, e
	}
	if len(j.Entries) > 4001 {
		return j, errors.New("journal too large")
	}
	seen := map[string]bool{}
	for _, v := range j.Entries {
		if v.Path != versionFile && !updatepath.Valid(v.Path) {
			return j, errors.New("invalid journal path")
		}
		n := strings.ToLower(v.Path)
		if seen[n] || len(v.NewHash) != 64 || v.Had && len(v.OldHash) != 64 {
			return j, errors.New("invalid journal entry")
		}
		seen[n] = true
	}
	return j, nil
}

// safeTarget rejects existing links and non-regular destination files. os.Root
// enforces the directory boundary during the subsequent filesystem operations.
func safeTarget(r *os.Root, name string) error {
	parts := strings.Split(name, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		st, e := r.Lstat(p)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("destination contains a link")
		}
		if i < len(parts)-1 && !st.IsDir() {
			return errors.New("destination parent is not a directory")
		}
		if i == len(parts)-1 && !st.Mode().IsRegular() {
			return errors.New("destination is not a regular file")
		}
	}
	return nil
}
func stage(r *os.Root, name string, input io.Reader, size int64) (entry, error) {
	var v entry
	v.Path = name
	if size < 0 || size > maxExpanded {
		return v, errors.New("invalid expanded file size")
	}
	if e := r.MkdirAll(work+"/stage/"+path.Dir(name), 0700); e != nil {
		return v, e
	}
	f, e := r.OpenFile(work+"/stage/"+name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return v, e
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(input, size+1))
	if e == nil && n != size {
		e = errors.New("expanded size mismatch")
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	v.NewHash = hex.EncodeToString(h.Sum(nil))
	return v, e
}

// Apply stages and validates all files before replacing any existing file.
// An interrupted update leaves a journal and blocks further updates until
// Rollback succeeds. This is recoverable multi-file installation, not a claim
// of atomicity across power loss. Retained backups are local sensitive data.
func Apply(p Plan) (err error) {
	if p.Size < 1 || p.Size > 1<<30 || len(p.SHA256) != 64 || p.Project == "" || p.Version == "" || p.Sequence < 1 {
		return errors.New("invalid installation plan")
	}
	if p.PackageKind != "full" && p.PackageKind != "file" {
		return errors.New("only full ZIP and single-file packages are supported")
	}
	r, e := open(p.InstallDir)
	if e != nil {
		return e
	}
	defer r.Close()
	if p.PackageKind == "file" && (!updatepath.Valid(p.TargetPath) || p.BaseVersion == "" || p.BaseVersion != p.CurrentVersion) {
		return errors.New("file package requires matching installed version and a safe relative target")
	}
	if b, e := r.ReadFile(versionFile); e == nil {
		var state installed
		if json.Unmarshal(b, &state) != nil || state.Project != p.Project || state.Sequence >= p.Sequence || p.PackageKind == "file" && state.Version != p.BaseVersion {
			return errors.New("local installed state does not permit this package")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	source, e := os.Open(p.Source)
	if e != nil {
		return e
	}
	defer source.Close()
	st, e := source.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return errors.New("source must be a regular file")
	}
	h, n, e := hashFile(source)
	if e != nil || n != p.Size || h != p.SHA256 {
		return errors.New("source SHA-256/size mismatch")
	}
	if _, e = source.Seek(0, io.SeekStart); e != nil {
		return e
	}
	// Keep previous committed backups; never silently delete a recoverable copy.
	if _, e = r.Lstat(work); e == nil {
		j, je := loadJournal(r)
		if je != nil || !j.Committed {
			return errors.New("pending update: run --rollback or inspect its journal first")
		}
		if e = r.Rename(work, ".firmware-hub-backup-"+rand.Text()); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = r.Mkdir(work, 0700); e != nil {
		return e
	}
	started := false
	defer func() {
		if err != nil && !started {
			_ = r.RemoveAll(work)
		}
	}()
	j := journal{}
	if p.PackageKind == "file" {
		v, e := stage(r, p.TargetPath, source, p.Size)
		if e != nil {
			return e
		}
		j.Entries = append(j.Entries, v)
	} else {
		z, e := zip.NewReader(source, p.Size)
		if e != nil {
			return errors.New("full desktop package must be a ZIP with application files at its root")
		}
		if len(z.File) > 4000 {
			return errors.New("too many ZIP entries")
		}
		total := int64(0)
		seen := map[string]bool{}
		for _, f := range z.File {
			if f.FileInfo().IsDir() {
				name := strings.TrimSuffix(f.Name, "/")
				if !updatepath.Valid(name) {
					return errors.New("unsafe ZIP directory")
				}
				continue
			}
			if !f.Mode().IsRegular() || !updatepath.Valid(f.Name) {
				return fmt.Errorf("unsafe ZIP member %q", f.Name)
			}
			lower := strings.ToLower(f.Name)
			if seen[lower] {
				return errors.New("case-insensitive ZIP collision")
			}
			seen[lower] = true
			if f.UncompressedSize64 > uint64(maxExpanded-total) {
				return errors.New("expanded ZIP exceeds 4 GiB limit")
			}
			total += int64(f.UncompressedSize64)
			in, e := f.Open()
			if e != nil {
				return e
			}
			v, e := stage(r, f.Name, in, int64(f.UncompressedSize64))
			in.Close()
			if e != nil {
				return e
			}
			j.Entries = append(j.Entries, v)
		}
		if len(j.Entries) == 0 {
			return errors.New("empty application package")
		}
	}
	sort.Slice(j.Entries, func(i, k int) bool { return j.Entries[i].Path < j.Entries[k].Path })
	b, _ := json.Marshal(installed{p.Project, p.Version, p.Sequence})
	v, e := stage(r, versionFile, strings.NewReader(string(b)), int64(len(b)))
	if e != nil {
		return e
	}
	j.Entries = append(j.Entries, v)
	for i := range j.Entries {
		v := &j.Entries[i]
		if e = safeTarget(r, v.Path); e != nil {
			return e
		}
		if old, e := fileHash(r, v.Path); e == nil {
			v.Had = true
			v.OldHash = old
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	if e = save(r, work+"/journal.json", j); e != nil {
		return e
	}
	started = true
	for _, v := range j.Entries {
		if e = safeTarget(r, v.Path); e != nil {
			break
		}
		if e = r.MkdirAll(path.Dir(v.Path), 0700); e != nil {
			break
		}
		if v.Had {
			if e = r.MkdirAll(work+"/backup/"+path.Dir(v.Path), 0700); e != nil {
				break
			}
			if e = r.Rename(v.Path, work+"/backup/"+v.Path); e != nil {
				break
			}
		}
		if e = r.Rename(work+"/stage/"+v.Path, v.Path); e != nil {
			break
		}
	}
	if e == nil {
		j.Committed = true
		e = save(r, work+"/journal.json", j)
	}
	if e != nil {
		if recovery := rollback(r, j); recovery != nil {
			return fmt.Errorf("install failed: %w; rollback incomplete: %v; backup retained", e, recovery)
		}
		return fmt.Errorf("install failed; original files restored: %w", e)
	}
	return nil
}
func rollback(r *os.Root, j journal) error {
	for i := len(j.Entries) - 1; i >= 0; i-- {
		v := j.Entries[i]
		backup := work + "/backup/" + v.Path
		bh, be := fileHash(r, backup)
		dh, de := fileHash(r, v.Path)
		if be == nil {
			if bh != v.OldHash {
				return errors.New("backup changed; manual recovery required")
			}
			if de == nil && dh != v.NewHash {
				return fmt.Errorf("destination changed: %s; manual recovery required", v.Path)
			}
			if de != nil && !os.IsNotExist(de) {
				return de
			}
			if de == nil {
				if e := r.Remove(v.Path); e != nil {
					return e
				}
			}
			if e := r.Rename(backup, v.Path); e != nil {
				return e
			}
		} else if !os.IsNotExist(be) {
			return be
		} else if !v.Had && de == nil {
			if dh != v.NewHash {
				return fmt.Errorf("new destination changed: %s", v.Path)
			}
			if e := r.Remove(v.Path); e != nil {
				return e
			}
		} else if v.Had && (de != nil || dh != v.OldHash) {
			return fmt.Errorf("original backup missing: %s", v.Path)
		} else if de != nil && !os.IsNotExist(de) {
			return de
		}
	}
	return r.RemoveAll(work)
}

// Rollback restores the latest update, refusing to overwrite files modified
// after installation. Older committed backups are left for manual retention.
func Rollback(dir string) error {
	r, e := open(dir)
	if e != nil {
		return e
	}
	defer r.Close()
	j, e := loadJournal(r)
	if e != nil {
		return e
	}
	return rollback(r, j)
}
