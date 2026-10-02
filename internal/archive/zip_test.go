package archive_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"shipr/internal/archive"
	"shipr/internal/testutil"
)

func mustOpen(t *testing.T, files map[string]string) *archive.Archive {
	t.Helper()
	b := testutil.Zip(t, files)
	a, err := archive.New(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func openEntries(t *testing.T, lim archive.Limits, entries []testutil.Entry) (*archive.Archive, error) {
	t.Helper()
	b := testutil.ZipEntries(t, entries)
	return archive.NewWithLimits(bytes.NewReader(b), int64(len(b)), lim)
}

func TestNestedDirectories(t *testing.T) {
	a := mustOpen(t, map[string]string{
		"README.md":        "hi",
		"web/package.json": "{}",
		"web/src/index.js": "x",
	})

	if got, want := a.Dirs(), []string{"", "web", "web/src"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Dirs() = %v, want %v", got, want)
	}
	if got, want := a.Children(""), []string{"README.md", "web"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Children(root) = %v, want %v", got, want)
	}
	if got, want := a.Children("web"), []string{"package.json", "src"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Children(web) = %v, want %v", got, want)
	}
	if !a.IsDir("web/src") || a.IsDir("web/package.json") {
		t.Error("IsDir gave wrong answers")
	}
	data, err := a.Read("web/package.json")
	if err != nil || string(data) != "{}" {
		t.Errorf("Read = %q, %v", data, err)
	}
}

func TestArchiveRootApplication(t *testing.T) {
	a := mustOpen(t, map[string]string{"package.json": "{}"})
	if !a.Has("package.json") {
		t.Fatal("root file not found")
	}
	if got, want := a.Children(""), []string{"package.json"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Children(root) = %v, want %v", got, want)
	}
}

func TestIgnoredDirectories(t *testing.T) {
	a := mustOpen(t, map[string]string{
		"node_modules/pkg/package.json": "{}",
		".git/config":                   "x",
		"__MACOSX/._a":                  "x",
		"src/index.js":                  "x",
	})
	if got, want := a.Dirs(), []string{"", "src"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Dirs() = %v, want %v", got, want)
	}
	if len(a.Warnings()) != 0 {
		t.Errorf("ignored dirs should not warn, got %v", a.Warnings())
	}
}

func TestUnsafeEntriesAreSkipped(t *testing.T) {
	a, err := openEntries(t, archive.DefaultLimits(), []testutil.Entry{
		{Name: "../../etc/passwd", Body: "x"},
		{Name: "/etc/shadow", Body: "x"},
		{Name: "ok/../../evil", Body: "x"},
		{Name: `C:\Windows\x`, Body: "x"},
		{Name: "bad\x1b[31mname", Body: "x"},
		{Name: "good.txt", Body: "ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Has("good.txt") {
		t.Error("safe entry was dropped")
	}
	for _, p := range []string{"etc/passwd", "etc/shadow", "evil"} {
		if a.Has(p) {
			t.Errorf("unsafe entry leaked as %q", p)
		}
	}
	if got := len(a.Warnings()); got != 5 {
		t.Errorf("want 5 warnings, got %d: %v", got, a.Warnings())
	}
	if !reflect.DeepEqual(a.Dirs(), []string{""}) {
		t.Errorf("Dirs() = %v", a.Dirs())
	}
}

func TestBackslashPathsAreNormalized(t *testing.T) {
	a, err := openEntries(t, archive.DefaultLimits(), []testutil.Entry{
		{Name: `app\package.json`, Body: "{}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Has("app/package.json") || !a.IsDir("app") {
		t.Error("backslash path was not normalized")
	}
}

func TestDuplicateKeepsFirst(t *testing.T) {
	a, err := openEntries(t, archive.DefaultLimits(), []testutil.Entry{
		{Name: "a.txt", Body: "first"},
		{Name: "a.txt", Body: "second"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := a.Read("a.txt")
	if err != nil || string(data) != "first" {
		t.Errorf("Read = %q, %v; want first", data, err)
	}
	if len(a.Warnings()) != 1 {
		t.Errorf("want 1 warning, got %v", a.Warnings())
	}
}

func TestSymlinkIsSkipped(t *testing.T) {
	a, err := openEntries(t, archive.DefaultLimits(), []testutil.Entry{
		{Name: "link", Body: "/etc/passwd", Mode: os.ModeSymlink | 0o777},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Has("link") || len(a.Warnings()) != 1 {
		t.Errorf("symlink handling wrong: has=%v warnings=%v", a.Has("link"), a.Warnings())
	}
}

func TestReadMissingFile(t *testing.T) {
	a := mustOpen(t, map[string]string{"a.txt": "x"})
	if _, err := a.Read("nope.txt"); !errors.Is(err, archive.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestOversizedRead(t *testing.T) {
	lim := archive.Limits{MaxEntries: 100, MaxFileSize: 1024, MaxDepth: 10}
	a, err := openEntries(t, lim, []testutil.Entry{
		{Name: "big.txt", Body: strings.Repeat("a", 4096)},
		{Name: "small.txt", Body: "ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Read("big.txt"); !errors.Is(err, archive.ErrTooLarge) {
		t.Errorf("big read err = %v, want ErrTooLarge", err)
	}
	if data, err := a.Read("small.txt"); err != nil || string(data) != "ok" {
		t.Errorf("small read = %q, %v", data, err)
	}
}

func TestDepthLimit(t *testing.T) {
	lim := archive.Limits{MaxEntries: 100, MaxFileSize: 1024, MaxDepth: 3}
	a, err := openEntries(t, lim, []testutil.Entry{
		{Name: "a/b/c.txt", Body: "x"},
		{Name: "a/b/c/d.txt", Body: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Has("a/b/c.txt") || a.Has("a/b/c/d.txt") {
		t.Error("depth limit not enforced")
	}
	if len(a.Warnings()) != 1 {
		t.Errorf("want 1 warning, got %v", a.Warnings())
	}
}

func TestTooManyEntries(t *testing.T) {
	lim := archive.Limits{MaxEntries: 2, MaxFileSize: 1024, MaxDepth: 10}
	_, err := openEntries(t, lim, []testutil.Entry{
		{Name: "a", Body: "x"}, {Name: "b", Body: "x"}, {Name: "c", Body: "x"},
	})
	if !errors.Is(err, archive.ErrTooManyEntries) {
		t.Errorf("err = %v, want ErrTooManyEntries", err)
	}
}

func TestWarningsAreCapped(t *testing.T) {
	var entries []testutil.Entry
	for i := 0; i < 100; i++ {
		entries = append(entries, testutil.Entry{Name: fmt.Sprintf("../x%d", i), Body: "x"})
	}
	a, err := openEntries(t, archive.DefaultLimits(), entries)
	if err != nil {
		t.Fatal(err)
	}
	w := a.Warnings()
	if len(w) != 51 || !strings.Contains(w[50], "50 more") {
		t.Errorf("want 50 warnings + summary, got %d (last: %q)", len(w), w[len(w)-1])
	}
}

func TestMalformedArchive(t *testing.T) {
	junk := "definitely not a zip"
	if _, err := archive.New(strings.NewReader(junk), int64(len(junk))); err == nil {
		t.Error("expected error for non-zip data")
	}
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()

	if _, err := archive.Open(filepath.Join(dir, "missing.zip")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file err = %v, want ErrNotExist", err)
	}
	if _, err := archive.Open(dir); err == nil {
		t.Error("expected error when opening a directory")
	}
	notZip := filepath.Join(dir, "not.zip")
	if err := os.WriteFile(notZip, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Open(notZip); err == nil {
		t.Error("expected error for non-zip file")
	}
}

func TestOpenValidFile(t *testing.T) {
	p := testutil.WriteZip(t, map[string]string{"package.json": "{}"})
	a, err := archive.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if !a.Has("package.json") {
		t.Error("file missing after Open")
	}
}