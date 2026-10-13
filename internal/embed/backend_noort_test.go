//go:build !cgo || (!ORT && !ALL)

package embed

import (
	"context"
	"strings"
	"testing"
)

func TestLocalRequiresNativeRuntime(t *testing.T) {
	session, backend, err := newBestSession(context.Background())
	if session != nil || backend != "" || err == nil {
		t.Fatalf("got session %v, backend %q, error %v", session, backend, err)
	}
	for _, want := range []string{"ONNX Runtime", "-tags ORT", "-embed http", "-embed none"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}
