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

// maxJobAttempts is the total tries (1 initial + retries) before a job is
// failed. The runners are idempotent/convergent, so retrying is safe.
const maxJobAttempts = 3

// Runner executes the work for a job. It is supplied by the VM service, which
// switches on job.Type. A non-nil error fails the job.
type Runner func(ctx context.Context, job *model.Job) error

// Queue is an in-process worker pool backed by the persistent job store.
type Queue struct {
	store       store.Store
	logger      *slog.Logger
	runner      Runner
	workers     int
	maxAttempts int
	backoffBase time.Duration

	ch     chan string
	stopCh chan struct{}
	wg     sync.WaitGroup
	once   sync.Once

	// jobCtx is cancelled on Stop so in-flight jobs abort promptly instead of
	// blocking the drain up to jobTimeout.
	jobCtx    context.Context
	jobCancel context.CancelFunc
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
		store:       st,
		logger:      logger,
		workers:     workers,
		maxAttempts: maxJobAttempts,
		backoffBase: time.Second,
		ch:          make(chan string, 256),
		stopCh:      make(chan struct{}),
	}
}

// SetRunner installs the job runner. Call before Start.
func (q *Queue) SetRunner(r Runner) { q.runner = r }

// Start launches the workers and re-queues any jobs left pending by a previous
// run. Interrupted jobs are settled by recover().
func (q *Queue) Start(ctx context.Context) {
	q.jobCtx, q.jobCancel = context.WithCancel(ctx)
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker()
	}
	q.recover(ctx)
}

// Stop cancels in-flight jobs, signals the workers, and waits for them.
func (q *Queue) Stop() {
	q.once.Do(func() {
		if q.jobCancel != nil {
			q.jobCancel()
		}
		close(q.stopCh)
	})
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
	// State persistence must succeed even during shutdown, so it uses a
	// background context; the run itself uses a cancellable, bounded context.
	persistCtx := context.Background()

	job, err := q.store.GetJob(persistCtx, id)
	if err != nil {
		q.logger.Error("job lookup failed", "job_id", id, "error", err)
		return
	}
	if job.State == model.JobSucceeded || job.State == model.JobFailed {
		return
	}

	runCtx, cancel := context.WithTimeout(q.jobCtx, jobTimeout)
	defer cancel()

	started := time.Now().UTC()
	job.State = model.JobRunning
	job.StartedAt = &started
	if err := q.store.UpdateJob(persistCtx, job); err != nil {
		q.logger.Error("mark job running failed", "job_id", id, "error", err)
		return
	}

	runErr := q.run(runCtx, job)

	// Interrupted by shutdown: leave the job 'running' so the next boot's
	// recover() settles it (and the VM reconciler cleans up).
	if runErr != nil && (errors.Is(runErr, context.Canceled) || q.jobCtx.Err() != nil) {
		q.logger.Warn("job interrupted by shutdown", "job_id", id, "type", job.Type)
		return
	}

	// Retry transient failures with backoff before giving up.
	if runErr != nil && job.Attempts+1 < q.maxAttempts {
		job.Attempts++
		job.State = model.JobQueued
		job.StartedAt = nil
		if err := q.store.UpdateJob(persistCtx, job); err != nil {
			q.logger.Error("requeue job failed", "job_id", id, "error", err)
			return
		}
		q.logger.Warn("job failed, retrying", "job_id", id, "type", job.Type, "attempt", job.Attempts, "error", runErr)
		q.scheduleAfter(id, q.backoff(job.Attempts))
		return
	}

	finished := time.Now().UTC()
	job.FinishedAt = &finished
	if runErr != nil {
		job.State = model.JobFailed
		job.Error = runErr.Error()
		q.logger.Error("job failed", "job_id", id, "type", job.Type, "vm_id", job.VMID, "attempts", job.Attempts+1, "error", runErr)
	} else {
		job.State = model.JobSucceeded
		q.logger.Info("job succeeded", "job_id", id, "type", job.Type, "vm_id", job.VMID)
	}
	if err := q.store.UpdateJob(persistCtx, job); err != nil {
		q.logger.Error("mark job done failed", "job_id", id, "error", err)
	}
}

// scheduleAfter schedules a job id after delay d, unless the queue is stopping.
func (q *Queue) scheduleAfter(id string, d time.Duration) {
	go func() {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
			q.schedule(id)
		case <-q.stopCh:
		}
	}()
}

// backoff returns the delay before retry attempt n: backoffBase << n, capped.
func (q *Queue) backoff(attempt int) time.Duration {
	d := q.backoffBase << uint(attempt)
	if max := 30 * time.Second; d > max {
		d = max
	}
	return d
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
