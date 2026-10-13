package embed

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalBoundsInferenceAndPreservesOrder(t *testing.T) {
	texts := make([]string, 100)
	for i := range texts {
		texts[i] = strconv.Itoa(i)
	}
	calls := 0
	l := &Local{run: func(_ context.Context, batch []string) ([][]float32, error) {
		if len(batch) != 1 {
			t.Fatalf("inference received %d inputs; want one", len(batch))
		}
		i, _ := strconv.Atoi(batch[0])
		v := make([]float32, localDims)
		v[i] = 1
		calls++
		return [][]float32{v}, nil
	}}
	vectors, err := l.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != len(texts) || calls != len(texts) {
		t.Fatalf("got %d vectors and %d calls", len(vectors), calls)
	}
	for i, v := range vectors {
		if v[i] != 1 {
			t.Fatalf("vector %d is out of order", i)
		}
	}
}

func TestLocalSerializesInference(t *testing.T) {
	var active atomic.Int32
	l := &Local{run: func(_ context.Context, _ []string) ([][]float32, error) {
		if active.Add(1) != 1 {
			t.Error("concurrent model inference")
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
		return [][]float32{make([]float32, localDims)}, nil
	}}
	var calls sync.WaitGroup
	for range 20 {
		calls.Go(func() {
			if _, err := l.Embed(context.Background(), []string{"text"}); err != nil {
				t.Error(err)
			}
		})
	}
	calls.Wait()
}

func TestLocalBackendRemainsResponsiveDuringInference(t *testing.T) {
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	l := &Local{run: func(_ context.Context, _ []string) ([][]float32, error) {
		close(started)
		<-release
		return [][]float32{make([]float32, localDims)}, nil
	}}
	l.backend.Store(BackendORT)
	go func() {
		defer close(done)
		_, _ = l.Embed(context.Background(), []string{"text"})
	}()
	<-started
	defer func() { close(release); <-done }()
	status := make(chan Backend, 1)
	go func() { status <- l.Backend() }()
	select {
	case backend := <-status:
		if backend != BackendORT {
			t.Errorf("backend = %q", backend)
		}
	case <-time.After(time.Second):
		t.Fatal("backend status blocked behind inference")
	}
}

func TestLocalStopsBetweenInputs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	l := &Local{run: func(_ context.Context, _ []string) ([][]float32, error) {
		calls++
		cancel()
		return [][]float32{make([]float32, localDims)}, nil
	}}
	_, err := l.Embed(ctx, []string{"first", "second"})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("got %d calls and error %v", calls, err)
	}
}
