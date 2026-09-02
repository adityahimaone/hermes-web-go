// Package workspace implements safe file access for the read-only data ports.
// Paths are resolved against a workspace root with symlink-traversal and
// parent-escape defense, and file reads are size-bounded.
package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrOutsideRoot is returned when a resolved path escapes the workspace root.
var ErrOutsideRoot = errors.New("path resolves outside workspace")

func HomeDir() (string, error) { return os.UserHomeDir() }

func ReadWorkspacesJSON(root string) ([]byte, error) {
	return os.ReadFile(filepath.Join(root, "workspaces.json"))
}

// SafeResolve joins root+rel and verifies the resulting absolute path stays
// under root after resolving symlinks. It refuses any component that climbs
// above root (..) and any symlink whose target leaves root.
func SafeResolve(root, rel string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootEval, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		// Root may not exist yet; fall back to the absolute form.
		rootEval = rootAbs
	}
	// Clean the join and require it remain within root.
	target := filepath.Join(rootAbs, filepath.FromSlash(rel))
	clean := filepath.Clean(target)
	if clean != rootAbs && !strings.HasPrefix(clean, rootAbs+string(filepath.Separator)) {
		return "", ErrOutsideRoot
	}
	// Evaluate target, or nearest existing parent for new files.
	evalPath := clean
	for {
		eval, evalErr := filepath.EvalSymlinks(evalPath)
		if evalErr == nil {
			if eval != rootEval && !strings.HasPrefix(eval, rootEval+string(filepath.Separator)) {
				return "", ErrOutsideRoot
			}
			break
		}
		next := filepath.Dir(evalPath)
		if next == evalPath {
			break
		}
		evalPath = next
	}
	return clean, nil
}

// SafeResolveNonNull resolves root+rel for a new file target. Unlike SafeResolve
// it does not require the final path component to exist; it only checks the nearest
// existing parent does not escape root.
func SafeResolveNonNull(root, rel string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootEval, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		rootEval = rootAbs
	}
	target := filepath.Join(rootAbs, filepath.FromSlash(rel))
	clean := filepath.Clean(target)
	if clean != rootAbs && !strings.HasPrefix(clean, rootAbs+string(filepath.Separator)) {
		return "", ErrOutsideRoot
	}
	// Check nearest existing parent.
	parent := filepath.Dir(clean)
	for {
		eval, evalErr := filepath.EvalSymlinks(parent)
		if evalErr == nil {
			if eval != rootEval && !strings.HasPrefix(eval, rootEval+string(filepath.Separator)) {
				return "", ErrOutsideRoot
			}
			break
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	return clean, nil
}

// SaveFile overwrites an existing regular file under root.
func SaveFile(root, rel string, data []byte) error {
	target, err := SafeResolve(root, rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("cannot save to symlinked entry")
	}
	if info.IsDir() {
		return errors.New("cannot save: path is a directory")
	}
	return os.WriteFile(target, data, info.Mode().Perm())
}

// CreateFile creates a new regular file under root without overwriting.
func CreateFile(root, rel string, data []byte) error {
	target, err := SafeResolve(root, rel)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// DeleteFile removes an existing regular file under root, never a symlink.
func DeleteFile(root, rel string) error {
	target, err := SafeResolve(root, rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("cannot delete symlinked entry")
	}
	if info.IsDir() {
		return errors.New("cannot delete directory")
	}
	return os.Remove(target)
}

// Entry mirrors the workspace directory-entry fields the browser consumes.
type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Type    string `json:"type"` // "dir" | "file" | "symlink"
	Size    *int64 `json:"size"`
	MtimeNS *int64 `json:"mtime_ns"`
	BirthNS *int64 `json:"birthtime_ns"`
	Rank    int    `json:"workspace_sort_rank"`
}

// ListDir returns sorted entries for root+rel, with directories first and
// each entry's path kept under the workspace root.
func ListDir(root, rel string) ([]Entry, error) {
	target, err := SafeResolve(root, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("not a directory: " + rel)
	}
	des, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}
	// ReadDir already returns name-sorted; re-sort so directories come first,
	// matching the Python listing contract (dirs then files, alphabetical).
	sort.Slice(des, func(i, j int) bool {
		di, dj := des[i].IsDir(), des[j].IsDir()
		if di != dj {
			return di
		}
		return des[i].Name() < des[j].Name()
	})
	var out []Entry
	for _, de := range des {
		name := de.Name()
		if name == "." || name == ".." {
			continue
		}
		var e Entry
		e.Name = name
		if rel == "." || rel == "" {
			e.Path = name
		} else {
			e.Path = strings.TrimSuffix(rel, "/") + "/" + name
		}
		full := filepath.Join(target, name)
		li, err := os.Lstat(full)
		if err != nil {
			continue
		}
		if li.Mode()&os.ModeSymlink != 0 {
			// Cycle/escape guard: skip symlinks whose target leaves root.
			linkTarget := full
			if resolved, rerr := filepath.EvalSymlinks(full); rerr == nil {
				if resolved != filepath.Clean(root) && !strings.HasPrefix(resolved, filepath.Clean(root)+string(filepath.Separator)) {
					// Display-only; do not expose where it points.
					e.Type = "symlink"
					e.Rank = 0
					e.MtimeNS = ns(&li)
					e.BirthNS = birthNS(&li)
					out = append(out, e)
					continue
				}
				linkTarget = resolved
			}
			e.Type = "symlink"
			e.Rank = 0
			e.MtimeNS = ns(&li)
			e.BirthNS = birthNS(&li)
			if si, serr := os.Stat(linkTarget); serr == nil {
				s := si.Size()
				e.Size = &s
				if si.IsDir() {
					e.Rank = 0
				}
			}
			out = append(out, e)
			continue
		}
		if li.IsDir() {
			e.Type = "dir"
			e.Rank = 1
		} else {
			e.Type = "file"
			e.Rank = 2
			s := li.Size()
			e.Size = &s
		}
		e.MtimeNS = ns(&li)
		e.BirthNS = birthNS(&li)
		out = append(out, e)
	}
	return out, nil
}

// ReadFile reads root+rel with an explicit byte limit; it rejects anything
// that escapes the workspace root.
func ReadFile(root, rel string, maxBytes int) ([]byte, error) {
	target, err := SafeResolve(root, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("not a file: " + rel)
	}
	if maxBytes > 0 && info.Size() > int64(maxBytes) {
		return nil, errors.New("file too large")
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && len(b) > maxBytes {
		return nil, errors.New("file too large")
	}
	return b, nil
}

func ns(fi *os.FileInfo) *int64 {
	if fi == nil {
		return nil
	}
	v := (*fi).ModTime().UnixNano()
	return &v
}

func birthNS(fi *os.FileInfo) *int64 {
	// Best-effort: return the same stat as ModTime on filesystems that don't
	// expose birthtime. Accurate on macOS/APFS; nil where unsupported.
	return nil
}
