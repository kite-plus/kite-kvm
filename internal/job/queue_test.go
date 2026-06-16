package job

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kite-plus/kite-kvm/internal/model"
	"github.com/kite-plus/kite-kvm/internal/store"
)

func newStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// waitTerminal polls until the job reaches a terminal state or times out.
func waitTerminal(t *testing.T, st store.Store, id string) *model.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := st.GetJob(context.Background(), id)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if job.State == model.JobSucceeded || job.State == model.JobFailed {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach terminal state", id)
	return nil
}

func TestQueueRunsJobToSuccess(t *testing.T) {
	st := newStore(t)
	q := NewQueue(st, 2, nil)

	var mu sync.Mutex
	var ran []string
	q.SetRunner(func(ctx context.Context, job *model.Job) error {
		mu.Lock()
		ran = append(ran, job.ID)
		mu.Unlock()
		return nil
	})
	q.Start(context.Background())
	defer q.Stop()

	j := New(model.JobCreate, "vm1", "")
	if err := q.Enqueue(context.Background(), j); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	done := waitTerminal(t, st, j.ID)
	if done.State != model.JobSucceeded {
		t.Errorf("state = %s, want succeeded", done.State)
	}
	if done.StartedAt == nil || done.FinishedAt == nil {
		t.Error("timestamps not set")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 1 {
		t.Errorf("runner invoked %d times, want 1", len(ran))
	}
}

func TestQueueRecordsFailure(t *testing.T) {
	st := newStore(t)
	q := NewQueue(st, 1, nil)
	q.maxAttempts = 1 // no retries: fail on first error
	q.SetRunner(func(ctx context.Context, job *model.Job) error {
		return errors.New("boom")
	})
	q.Start(context.Background())
	defer q.Stop()

	j := New(model.JobStart, "vm1", "")
	_ = q.Enqueue(context.Background(), j)
	done := waitTerminal(t, st, j.ID)
	if done.State != model.JobFailed {
		t.Errorf("state = %s, want failed", done.State)
	}
	if done.Error != "boom" {
		t.Errorf("error = %q, want boom", done.Error)
	}
}

func TestQueueRetriesThenSucceeds(t *testing.T) {
	st := newStore(t)
	q := NewQueue(st, 1, nil)
	q.backoffBase = time.Millisecond // fast retries for the test
	var calls int32
	q.SetRunner(func(ctx context.Context, job *model.Job) error {
		if atomic.AddInt32(&calls, 1) == 1 {
			return errors.New("transient")
		}
		return nil
	})
	q.Start(context.Background())
	defer q.Stop()

	j := New(model.JobReboot, "vm1", "")
	_ = q.Enqueue(context.Background(), j)
	done := waitTerminal(t, st, j.ID)
	if done.State != model.JobSucceeded {
		t.Errorf("state = %s, want succeeded after retry", done.State)
	}
	if done.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (one retry)", done.Attempts)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("runner called %d times, want 2", n)
	}
}

func TestQueueRecoversQueuedAndRunning(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()

	// Simulate a crash: a queued job and a job stuck in running.
	queued := New(model.JobCreate, "vmQ", "")
	if err := st.CreateJob(ctx, queued); err != nil {
		t.Fatal(err)
	}
	stuck := New(model.JobStart, "vmR", "")
	stuck.State = model.JobRunning
	if err := st.CreateJob(ctx, stuck); err != nil {
		t.Fatal(err)
	}

	q := NewQueue(st, 1, nil)
	var ran bool
	q.SetRunner(func(ctx context.Context, job *model.Job) error {
		ran = true
		return nil
	})
	q.Start(ctx)
	defer q.Stop()

	// The queued job is re-run to success.
	if done := waitTerminal(t, st, queued.ID); done.State != model.JobSucceeded {
		t.Errorf("queued job state = %s, want succeeded", done.State)
	}
	if !ran {
		t.Error("recovered queued job was not run")
	}
	// The interrupted running job is marked failed, not re-run.
	got, _ := st.GetJob(ctx, stuck.ID)
	if got.State != model.JobFailed {
		t.Errorf("interrupted job state = %s, want failed", got.State)
	}
}
