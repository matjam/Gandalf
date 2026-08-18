package embed

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindORTLibraryDirPrefersAnExplicitSetting keeps a machine with the
// library somewhere unusual from being stuck on the slow path.
func TestFindORTLibraryDirPrefersAnExplicitSetting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(LibraryPathEnv, dir)

	if got := FindORTLibraryDir(); got != dir {
		t.Errorf("FindORTLibraryDir() = %q, want the configured %q", got, dir)
	}
}

// TestFindORTLibraryDirFindsAnInstalledLibrary covers the discovery path
// without depending on what this machine happens to have installed.
func TestFindORTLibraryDirFindsAnInstalledLibrary(t *testing.T) {
	dir := t.TempDir()

	names := ortLibraryNames()
	if len(names) == 0 {
		t.Skip("no library names known for this platform")
	}

	// Nothing there yet: an empty directory must not be mistaken for a hit.
	t.Setenv(LibraryPathEnv, "")
	if got := findInDirs([]string{dir}); got != "" {
		t.Errorf("found %q in an empty directory", got)
	}

	if err := os.WriteFile(filepath.Join(dir, names[0]), []byte("not really a library"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := findInDirs([]string{dir}); got != dir {
		t.Errorf("findInDirs = %q, want %q", got, dir)
	}
}

// TestSearchDirsAreAbsolute guards against a relative entry, which would make
// discovery depend on the working directory the server happened to start in.
func TestSearchDirsAreAbsolute(t *testing.T) {
	for _, dir := range ortSearchDirs() {
		if !filepath.IsAbs(dir) {
			t.Errorf("search dir %q is relative", dir)
		}
	}
}
