package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeJobNotifier struct {
	ch  chan string
	err error
}

func (f *fakeJobNotifier) ListenJobInsert(ctx context.Context) (<-chan string, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.ch == nil {
		f.ch = make(chan string, 1)
	}
	return f.ch, nil
}

type jobNotifyStore struct {
	fakeStore
	notifier *fakeJobNotifier
}

func (s *jobNotifyStore) ListenJobInsert(ctx context.Context) (<-chan string, error) {
	return s.notifier.ListenJobInsert(ctx)
}

func TestOpenJobNotifySuccess(t *testing.T) {
	t.Parallel()
	srv := New(&jobNotifyStore{notifier: &fakeJobNotifier{}}, fakeEmbedder{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := srv.openJobNotify(ctx)
	if ch == nil {
		t.Fatal("openJobNotify() = nil, want channel")
	}
}

func TestOpenJobNotifyFailure(t *testing.T) {
	t.Parallel()
	srv := New(&jobNotifyStore{notifier: &fakeJobNotifier{err: errors.New("listen failed")}}, fakeEmbedder{}, nil)
	ch := srv.openJobNotify(context.Background())
	if ch != nil {
		t.Fatalf("openJobNotify() = %v, want nil on error", ch)
	}
}

func TestWaitForJobContextCancel(t *testing.T) {
	t.Parallel()
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv.waitForJob(ctx, nil)
}

func TestWaitForJobNotification(t *testing.T) {
	t.Parallel()
	srv := New(&fakeStore{}, fakeEmbedder{}, nil)
	ch := make(chan string, 1)
	ch <- "42"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	srv.waitForJob(ctx, ch)
}

func TestStartWorkersSharedNotify(t *testing.T) {
	t.Parallel()
	notifier := &fakeJobNotifier{ch: make(chan string, 1)}
	st := &jobNotifyStore{notifier: notifier}
	srv := New(st, fakeEmbedder{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.StartWorkers(ctx, 2, t.TempDir())
	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
}
