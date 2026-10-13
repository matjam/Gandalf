//go:build cgo && (ORT || ALL)

package embed

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestLocalNativeMemoryPlateau uses only synthetic text and a cached model.
// Run explicitly with GANDALF_MEMORY_TESTS=1 on Linux, where current RSS is
// observable. It checks repeated native calls after allocator warmup rather
// than comparing a loaded runtime with an unloaded process.
func TestLocalNativeMemoryPlateau(t *testing.T) {
	if os.Getenv("GANDALF_MEMORY_TESTS") == "" || runtime.GOOS != "linux" {
		t.Skip("set GANDALF_MEMORY_TESTS=1 on Linux to check native memory")
	}
	l := NewLocal("")
	defer l.Close()
	ctx := context.Background()
	texts := []string{strings.Repeat("A synthetic document describes a forest and a river. ", 150)}
	var expected Vector
	for i := range 10 {
		vectors, err := l.Embed(ctx, texts)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			expected = vectors[0]
		}
		if Cosine(expected, vectors[0]) < 0.99999 {
			t.Fatal("embedding changed across identical requests")
		}
	}
	if l.Backend() != BackendORT {
		t.Fatalf("backend = %s, want native ONNX Runtime", l.Backend())
	}
	runtime.GC()
	baseline := nativeTestRSS(t)
	peak := baseline
	for i := range 200 {
		vectors, err := l.Embed(ctx, texts)
		if err != nil {
			t.Fatal(err)
		}
		if Cosine(expected, vectors[0]) < 0.99999 {
			t.Fatalf("embedding changed at iteration %d", i)
		}
		if i%20 == 19 {
			runtime.GC()
			peak = max(peak, nativeTestRSS(t))
		}
	}
	const maxGrowth = 128 * 1024 * 1024
	if peak-baseline > maxGrowth {
		t.Fatalf("native RSS grew %d bytes after warmup; maximum %d", peak-baseline, maxGrowth)
	}
	t.Logf("native RSS warmup=%d peak=%d growth=%d bytes", baseline, peak, peak-baseline)
}

func nativeTestRSS(t *testing.T) int64 {
	t.Helper()
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] != "kB" {
			t.Fatal(fmt.Errorf("unexpected RSS field %q", line))
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return kb * 1024
	}
	t.Fatal("VmRSS missing from /proc/self/status")
	return 0
}
