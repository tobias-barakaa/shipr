// Package testutil builds zip archives for tests.
package testutil

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Entry is one zip entry. A zero Mode means a regular file.
type Entry struct {
	Name string
	Body string
	Mode os.FileMode
}

func sorted(files map[string]string) []Entry {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	entries := make([]Entry, len(names))
	for i, n := range names {
		entries[i] = Entry{Name: n, Body: files[n]}
	}
	return entries
}

// ZipEntries builds a zip with entries in the given order (duplicates allowed).
func ZipEntries(t testing.TB, entries []Entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.Name, Method: zip.Deflate}
		if e.Mode != 0 {
			h.SetMode(e.Mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatalf("zip entry %q: %v", e.Name, err)
		}
		if _, err := w.Write([]byte(e.Body)); err != nil {
			t.Fatalf("zip entry %q: %v", e.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Zip builds a zip from a name->content map.
func Zip(t testing.TB, files map[string]string) []byte {
	t.Helper()
	return ZipEntries(t, sorted(files))
}

// WriteEntries writes a zip to a temp dir as "sample.zip" and returns its path.
func WriteEntries(t testing.TB, entries []Entry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sample.zip")
	if err := os.WriteFile(p, ZipEntries(t, entries), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// WriteZip is WriteEntries for a name->content map.
func WriteZip(t testing.TB, files map[string]string) string {
	t.Helper()
	return WriteEntries(t, sorted(files))
}
