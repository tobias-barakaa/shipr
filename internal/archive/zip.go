// Package archive provides a safe, read-only, in-memory view of a ZIP file.
// Nothing is ever extracted to disk.
package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"unicode"
)

var (
	ErrNotFound       = errors.New("file not found in archive")
	ErrTooLarge       = errors.New("file exceeds read limit")
	ErrTooManyEntries = errors.New("archive has too many entries")
)

// Limits bound the resources an untrusted archive can consume.
type Limits struct {
	MaxEntries  int   // entries in the central directory
	MaxFileSize int64 // largest file Read will return
	MaxDepth    int   // deepest allowed path
}

// DefaultLimits returns the limits used by Open and New.
func DefaultLimits() Limits {
	return Limits{MaxEntries: 500_000, MaxFileSize: 1 << 20, MaxDepth: 64}
}

const maxWarnings = 50

// Generated or vendored folders that never hold the project's own apps.
var ignoredDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"vendor":       true,
	"venv":         true,
	".venv":        true,
	"__pycache__":  true,
	"__MACOSX":     true,
	"dist":         true,
	".next":        true,
	".astro":       true,
	".idea":        true,
	".vscode":      true,
}

// Archive is a read-only view of a zip's tree. Paths use forward slashes;
// the archive root is "".
type Archive struct {
	closer     io.Closer
	limits     Limits
	files      map[string]*zip.File
	dirs       map[string]struct{}
	children   map[string][]string
	warnings   []string
	suppressed int
}

// Open opens a zip file from disk.
func Open(zipPath string) (*Archive, error) {
	f, err := os.Open(zipPath)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		f.Close()
		return nil, fmt.Errorf("%s is a directory, not a zip file", zipPath)
	}
	a, err := New(f, info.Size())
	if err != nil {
		f.Close()
		return nil, err
	}
	a.closer = f
	return a, nil
}

// New reads a zip from any ReaderAt using DefaultLimits.
func New(r io.ReaderAt, size int64) (*Archive, error) {
	return NewWithLimits(r, size, DefaultLimits())
}

// NewWithLimits reads a zip using explicit limits.
func NewWithLimits(r io.ReaderAt, size int64, lim Limits) (*Archive, error) {
	zr, err := zip.NewReader(r, size)
	// Newer Go versions may return a usable reader plus ErrInsecurePath.
	// We validate every name ourselves, so that case is safe to continue.
	if err != nil && !(errors.Is(err, zip.ErrInsecurePath) && zr != nil) {
		return nil, fmt.Errorf("reading zip directory: %w", err)
	}
	if len(zr.File) > lim.MaxEntries {
		return nil, fmt.Errorf("%w: %d entries (limit %d)", ErrTooManyEntries, len(zr.File), lim.MaxEntries)
	}

	a := &Archive{
		limits: lim,
		files:  map[string]*zip.File{},
		dirs:   map[string]struct{}{"": {}},
	}
	for _, f := range zr.File {
		a.add(f)
	}
	a.indexChildren()
	return a, nil
}

func (a *Archive) Close() error {
	if a.closer == nil {
		return nil
	}
	return a.closer.Close()
}

// Has reports whether a file exists.
func (a *Archive) Has(p string) bool {
	_, ok := a.files[p]
	return ok
}

// IsDir reports whether a directory exists.
func (a *Archive) IsDir(p string) bool {
	_, ok := a.dirs[p]
	return ok
}

// Read returns a file's contents. Files over the size limit return ErrTooLarge
// rather than truncated data.
func (a *Archive) Read(p string) ([]byte, error) {
	f, ok := a.files[p]
	if !ok {
		return nil, fmt.Errorf("%s: %w", p, ErrNotFound)
	}
	limit := a.limits.MaxFileSize
	tooLarge := fmt.Errorf("%s: %w (limit %d bytes)", p, ErrTooLarge, limit)

	if f.UncompressedSize64 > uint64(limit) {
		return nil, tooLarge
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	defer rc.Close()

	// The declared size can lie, so cap the actual read too.
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", p, err)
	}
	if int64(len(data)) > limit {
		return nil, tooLarge
	}
	return data, nil
}

// Dirs returns every directory (including "" for the root), sorted.
func (a *Archive) Dirs() []string {
	out := make([]string, 0, len(a.dirs))
	for d := range a.dirs {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Children returns the sorted names of direct children (files and folders).
func (a *Archive) Children(dir string) []string {
	return append([]string(nil), a.children[dir]...)
}

// Warnings lists entries that were skipped as unsafe or unsupported.
func (a *Archive) Warnings() []string {
	out := append([]string(nil), a.warnings...)
	if a.suppressed > 0 {
		out = append(out, fmt.Sprintf("%d more entries skipped", a.suppressed))
	}
	return out
}

func (a *Archive) add(f *zip.File) {
	name, err := normalize(f.Name)
	if err != nil {
		a.warn("skipped entry %q: %v", f.Name, err)
		return
	}
	if name == "" || isIgnored(name) {
		return
	}
	if strings.Count(name, "/")+1 > a.limits.MaxDepth {
		a.warn("skipped entry %q: path deeper than %d levels", f.Name, a.limits.MaxDepth)
		return
	}

	mode := f.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		a.warn("skipped entry %q: symbolic link (never followed)", f.Name)
		return
	case a.ancestorIsFile(name):
		a.warn("skipped entry %q: parent path is a file", f.Name)
		return
	case mode.IsDir():
		if _, isFile := a.files[name]; isFile {
			a.warn("skipped entry %q: name already used by a file", f.Name)
			return
		}
		a.addDir(name)
		return
	case !mode.IsRegular():
		a.warn("skipped entry %q: unsupported entry type", f.Name)
		return
	}

	if _, dup := a.files[name]; dup {
		a.warn("skipped entry %q: duplicate path (keeping first)", f.Name)
		return
	}
	if _, isDir := a.dirs[name]; isDir {
		a.warn("skipped entry %q: name already used by a directory", f.Name)
		return
	}
	a.files[name] = f
	a.addDir(parent(name))
}

func (a *Archive) addDir(p string) {
	for d := p; d != ""; d = parent(d) {
		a.dirs[d] = struct{}{}
	}
}

func (a *Archive) ancestorIsFile(p string) bool {
	for d := parent(p); d != ""; d = parent(d) {
		if _, ok := a.files[d]; ok {
			return true
		}
	}
	return false
}

func (a *Archive) indexChildren() {
	a.children = map[string][]string{}
	for p := range a.files {
		a.children[parent(p)] = append(a.children[parent(p)], path.Base(p))
	}
	for p := range a.dirs {
		if p != "" {
			a.children[parent(p)] = append(a.children[parent(p)], path.Base(p))
		}
	}
	for k := range a.children {
		sort.Strings(a.children[k])
	}
}

func (a *Archive) warn(format string, args ...any) {
	if len(a.warnings) >= maxWarnings {
		a.suppressed++
		return
	}
	a.warnings = append(a.warnings, fmt.Sprintf(format, args...))
}

func parent(p string) string {
	d := path.Dir(p)
	if d == "." {
		return ""
	}
	return d
}

// normalize cleans an entry name and rejects unsafe ones.
// It returns "" (and no error) for entries that name the root itself.
func normalize(name string) (string, error) {
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("name contains control characters")
		}
	}
	n := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(n, "/") || hasDriveLetter(n) {
		return "", errors.New("absolute path")
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == ".." {
			return "", errors.New("path traversal (..)")
		}
	}
	clean := path.Clean(n)
	if clean == "." {
		return "", nil
	}
	return clean, nil
}

func hasDriveLetter(n string) bool {
	if len(n) < 2 || n[1] != ':' {
		return false
	}
	c := n[0] | 0x20
	return c >= 'a' && c <= 'z'
}

func isIgnored(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if ignoredDirs[seg] {
			return true
		}
	}
	return false
}
