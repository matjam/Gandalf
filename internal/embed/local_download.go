package embed

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gomlx/go-huggingface/hub"
	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/util/fileutil"
)

type modelFileDownloader interface {
	DownloadFilesCtx(context.Context, ...string) ([]string, error)
}

// downloadLocalModel retains Hugot's repository validation, but downloads each
// file separately. The hub's multi-file downloader races its progress counters,
// even when progress reporting is disabled or parallel connections are limited.
func downloadLocalModel(ctx context.Context, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repo := hub.New(LocalModel).WithProgressBar(false)
	repo.Verbosity = 0
	opts := hugot.NewDownloadOptions()
	files, err := hugot.ValidateDownloadedHFModel(repo, opts)
	if err != nil {
		return fmt.Errorf("validate model files: %w", err)
	}
	for attempt := range opts.MaxRetries {
		err = downloadModelFiles(ctx, repo, files, destination)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt+1 == opts.MaxRetries {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(opts.RetryInterval) * time.Second):
		}
	}
	return fmt.Errorf("download model files after %d attempts: %w", opts.MaxRetries, err)
}

func downloadModelFiles(ctx context.Context, repo modelFileDownloader, files []string, destination string) error {
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		paths, err := repo.DownloadFilesCtx(ctx, file)
		if err != nil {
			return fmt.Errorf("download %s: %w", file, err)
		}
		if len(paths) != 1 {
			return fmt.Errorf("download %s returned %d paths, expected one", file, len(paths))
		}
		source, err := filepath.EvalSymlinks(paths[0])
		if err != nil {
			return fmt.Errorf("resolve downloaded %s: %w", file, err)
		}
		if err := fileutil.CopyFile(ctx, source, filepath.Join(destination, filepath.Base(file))); err != nil {
			return fmt.Errorf("cache downloaded %s: %w", file, err)
		}
	}
	return nil
}
