package embed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/options"
)

// Backend names the compute path an in-process embedder ended up on.
//
// It is reported rather than assumed because the difference is two orders of
// magnitude, and because which one you get depends on how the binary was built
// and what is installed on the machine. A user wondering why indexing takes
// twenty minutes deserves to be told which engine is doing the work.
type Backend string

const (
	// BackendORT is ONNX Runtime through cgo: the native, optimised path.
	BackendORT Backend = "onnxruntime"
)

// LibraryPathEnv overrides the directory the ONNX Runtime shared library is
// found in.
const LibraryPathEnv = "GANDALF_ONNXRUNTIME"

// ortLibraryNames are the shared library filenames to look for, per platform.
func ortLibraryNames() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"libonnxruntime.dylib", "libonnxruntime.1.dylib"}
	case "windows":
		return []string{"onnxruntime.dll"}
	default:
		return []string{"libonnxruntime.so", "libonnxruntime.so.1"}
	}
}

// ortSearchDirs are the usual places a package manager leaves the library.
//
// Homebrew comes first on darwin because that is how the library gets onto a
// Mac in practice, and because its prefix is not on the default loader path —
// leaving discovery to the linker is what makes ORT silently unavailable on an
// otherwise correctly configured machine.
func ortSearchDirs() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/opt/homebrew/lib", // Apple silicon Homebrew
			"/usr/local/lib",    // Intel Homebrew, and manual installs
		}
	case "windows":
		return nil
	default:
		return []string{
			"/usr/local/lib",
			"/usr/lib",
			"/usr/lib/x86_64-linux-gnu",
			"/usr/lib/aarch64-linux-gnu",
		}
	}
}

// FindORTLibraryDir returns the directory holding the ONNX Runtime shared
// library, or the empty string when it is not installed anywhere we look.
//
// A directory rather than the library file itself: hugot's option validates
// that what it is given is a directory and appends the platform's library name
// itself, whatever its documentation says.
//
// An explicit setting always wins, so a machine with the library somewhere
// unusual does not have to move it to be fast.
func FindORTLibraryDir() string {
	if dir := os.Getenv(LibraryPathEnv); dir != "" {
		return dir
	}

	return findInDirs(ortSearchDirs())
}

// findInDirs returns the first directory holding a library we recognise.
func findInDirs(dirs []string) string {
	for _, dir := range dirs {
		for _, name := range ortLibraryNames() {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return dir
			}
		}
	}
	return ""
}

// newBestSession requires the native runtime. Falling back to the Go backend
// retains an unbounded graph cache and can exhaust the host's memory.
func newBestSession(ctx context.Context) (*hugot.Session, Backend, error) {
	var opts []options.WithOption
	if dir := FindORTLibraryDir(); dir != "" {
		opts = append(opts, options.WithOnnxLibraryPath(dir))
	}

	session, err := hugot.NewORTSession(ctx, opts...)
	if err != nil {
		return nil, "", fmt.Errorf("local embeddings require ONNX Runtime and a binary built with -tags ORT: %w; use -embed http or -embed none when native inference is unavailable", err)
	}
	return session, BackendORT, nil
}
