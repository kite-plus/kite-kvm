package job

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/kite-plus/kite-kvm/internal/model"
	"github.com/kite-plus/kite-kvm/internal/store"
)

var errNoRunner = errors.New("job: no runner configured")

// jobTimeout bounds how long a single job may run before its context is
// cancelled. VM operations (boot, provision) are slow but not unbounded.
const jobTimeout = 15 * time.Minute

// Runner executes the work for a job. It is supplied by the VM service, which
// switches on job.Type. A non-nil error fails the job.
type Runner func(ctx context.Context, job *model.Job) error

// Queue is an in-process worker pool backed by the persistent job store.
type Queue struct {
	store   store.Store
	logger  *slog.Logger
	runner  Runner
	workers int

	ch     chan string
	stopCh chan struct{}
	wg     sync.WaitGroup
	once   sync.Once
}

// NewQueue constructs a Queue with the given worker count.
func NewQueue(st store.Store, workers int, logger *slog.Logger) *Queue {
	if workers <= 0 {
		workers = 4
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Queue{
		store:   st,
		logger:  logger,
		workers: workers,
		ch:      make(chan string, 256),
		stopCh:  make(chan struct{}),
	}
}

// SetRunner installs the job runner. Call before Start.
func (q *Queue) SetRunner(r Runner) { q.runner = r }

// Start launches the workers and re-queues any jobs left pending by a previous
// run. Jobs that were mid-flight at shutdown are marked failed.
func (q *Queue) Start(ctx context.Context) {
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker()
	}
	q.recover(ctx)
}

// Stop signals the workers to finish their current job and waits for them.
func (q *Queue) Stop() {
	q.once.Do(func() { close(q.stopCh) })
	q.wg.Wait()
}

// Enqueue persists a queued job and schedules it for execution.
func (q *Queue) Enqueue(ctx context.Context, job *model.Job) error {
	job.State = model.JobQueued
	if err := q.store.CreateJob(ctx, job); err != nil {
		return err
	}
	q.schedule(job.ID)
	return nil
}

func (q *Queue) schedule(id string) {
	go func() {
		select {
		case q.ch <- id:
		case <-q.stopCh:
		}
	}()
}

func (q *Queue) worker() {
	defer q.wg.Done()
	for {
		select {
		case <-q.stopCh:
			return
		case id := <-q.ch:
			q.process(id)
		}
	}
}

func (q *Queue) process(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()

	job, err := q.store.GetJob(ctx, id)
	if err != nil {
		q.logger.Error("job lookup failed", "job_id", id, "error", err)
		return
	}
	// Skip jobs already in a terminal state (e.g. duplicate schedule).
	if job.State == model.JobSucceeded || job.State == model.JobFailed {
		return
	}

	started := time.Now().UTC()
	job.State = model.JobRunning
	job.StartedAt = &started
	if err := q.store.UpdateJob(ctx, job); err != nil {
		q.logger.Error("mark job running failed", "job_id", id, "error", err)
		return
	}

	runErr := q.run(ctx, job)

	finished := time.Now().UTC()
	job.FinishedAt = &finished
	if runErr != nil {
		job.State = model.JobFailed
		job.Error = runErr.Error()
		q.logger.Error("job failed", "job_id", id, "type", job.Type, "vm_id", job.VMID, "error", runErr)
	} else {
		job.State = model.JobSucceeded
		q.logger.Info("job succeeded", "job_id", id, "type", job.Type, "vm_id", job.VMID)
	}
	if err := q.store.UpdateJob(ctx, job); err != nil {
		q.logger.Error("mark job done failed", "job_id", id, "error", err)
	}
}

func (q *Queue) run(ctx context.Context, job *model.Job) error {
	if q.runner == nil {
		return errNoRunner
	}
	return q.runner(ctx, job)
}

func (q *Queue) recover(ctx context.Context) {
	running, err := q.store.ListJobsByState(ctx, model.JobRunning)
	if err != nil {
		q.logger.Error("recover: list running jobs failed", "error", err)
	}
	for _, j := range running {
		finished := time.Now().UTC()
		j.State = model.JobFailed
		j.Error = "interrupted by agent restart"
		j.FinishedAt = &finished
		if err := q.store.UpdateJob(ctx, j); err != nil {
			q.logger.Error("recover: fail running job", "job_id", j.ID, "error", err)
		}
	}

	queued, err := q.store.ListJobsByState(ctx, model.JobQueued)
	if err != nil {
		q.logger.Error("recover: list queued jobs failed", "error", err)
	}
	for _, j := range queued {
		q.schedule(j.ID)
	}
	if n := len(queued); n > 0 {
		q.logger.Info("recovered pending jobs", "count", n)
	}
}
