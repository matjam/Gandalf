package embed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type fileDownloadStub struct {
	call func(context.Context, ...string) ([]string, error)
}

func (s fileDownloadStub) DownloadFilesCtx(ctx context.Context, files ...string) ([]string, error) {
	return s.call(ctx, files...)
}

func TestModelFilesDownloadSeparately(t *testing.T) {
	source := filepath.Join(t.TempDir(), "cached")
	if err := os.WriteFile(source, []byte("model data"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requested []string
	repo := fileDownloadStub{call: func(_ context.Context, files ...string) ([]string, error) {
		if len(files) != 1 {
			t.Fatalf("parallel file request %v", files)
		}
		requested = append(requested, files[0])
		return []string{source}, nil
	}}
	dest := filepath.Join(t.TempDir(), "new-model")
	files := []string{"onnx/model.onnx", "tokenizer.json", "config.json"}
	if err := downloadModelFiles(context.Background(), repo, files, dest); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, requested) {
		t.Fatalf("downloaded %v, want %v", requested, files)
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dest, filepath.Base(file)))
		if err != nil || string(data) != "model data" {
			t.Fatalf("copied file %s: data %q, error %v", file, data, err)
		}
	}
}

func TestModelFileDownloadStopsOnErrorOrCancellation(t *testing.T) {
	for _, name := range []string{"error", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := errStub
			calls := 0
			if name == "cancelled" {
				cancel()
				want = context.Canceled
			}
			repo := fileDownloadStub{call: func(context.Context, ...string) ([]string, error) {
				calls++
				return nil, errStub
			}}
			err := downloadModelFiles(ctx, repo, []string{"first", "second"}, t.TempDir())
			if !errors.Is(err, want) || calls > 1 || (name == "cancelled" && calls != 0) {
				t.Fatalf("calls %d, error %v, want %v", calls, err, want)
			}
		})
	}
}
