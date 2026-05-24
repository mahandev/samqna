package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"samqna/model"
	"samqna/repository"

	"gorm.io/gorm"
)

type Pool struct {
	db          *gorm.DB
	sr          *repository.SubmissionRepo
	jr          *repository.JobRepo
	reg         *Registry
	workers     int
	pollEvery   time.Duration
	maxAttempts int

	// backoffOverride: when >0, used instead of repo's default backoff.
	// Used in tests to drive retries fast.
	backoffOverride time.Duration

	wg   sync.WaitGroup
	stop chan struct{}
}

func NewPool(db *gorm.DB, sr *repository.SubmissionRepo, jr *repository.JobRepo, reg *Registry, workers int, poll time.Duration, maxAttempts int) *Pool {
	return &Pool{
		db: db, sr: sr, jr: jr, reg: reg,
		workers: workers, pollEvery: poll, maxAttempts: maxAttempts,
		stop: make(chan struct{}),
	}
}

func (p *Pool) Start() {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.workerLoop(fmt.Sprintf("w%d", i))
	}
}

func (p *Pool) Stop(timeout time.Duration) {
	close(p.stop)
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		slog.Warn("pool stop timeout exceeded")
	}
	// release any leftover locks
	_, _ = p.jr.ReleaseStaleLocks(0)
}

func (p *Pool) workerLoop(id string) {
	defer p.wg.Done()
	t := time.NewTicker(p.pollEvery)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.tick(id)
		}
	}
}

func (p *Pool) tick(workerID string) {
	job, err := p.jr.Claim(workerID)
	if err != nil {
		slog.Error("claim", "err", err, "worker", workerID)
		return
	}
	if job == nil {
		return
	}
	sub, err := p.sr.Get(job.SubmissionID)
	if err != nil {
		slog.Error("load submission", "err", err, "job_id", job.ID)
		_ = p.jr.RecordFailure(job.ID, "load submission: "+err.Error())
		return
	}
	stage, ok := p.reg.Get(string(job.Stage))
	if !ok {
		_ = p.jr.MarkPermanentFailure(job.ID, "unknown stage: "+string(job.Stage))
		return
	}

	logger := slog.With("submission_id", sub.ID, "job_id", job.ID, "stage", job.Stage, "attempt", job.Attempts+1, "worker", workerID)
	logger.Info("stage start")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res := RunStage(ctx, stage, sub)

	if res.Err != nil {
		logger.Warn("stage failed", "err", res.Err)
		if job.Attempts+1 >= p.maxAttempts {
			_ = p.jr.MarkPermanentFailure(job.ID, res.Err.Error())
			sub.Status = model.StatusFailed
			_ = p.sr.Update(sub)
			logger.Error("permanent failure")
			return
		}
		if p.backoffOverride > 0 {
			// fast path for tests
			_ = p.db.Model(&model.Job{}).Where("id = ?", job.ID).Updates(map[string]any{
				"attempts":    job.Attempts + 1,
				"last_error":  res.Err.Error(),
				"status":      model.JobPending,
				"locked_by":   nil,
				"locked_at":   nil,
				"next_run_at": time.Now().Add(p.backoffOverride),
			}).Error
			return
		}
		_ = p.jr.RecordFailure(job.ID, res.Err.Error())
		return
	}
	if res.Terminal {
		if sub.Status != model.StatusQuarantined {
			sub.Status = model.StatusReady
			_ = p.sr.Update(sub)
		}
		_ = p.jr.Complete(job.ID)
		logger.Info("submission ready")
		return
	}
	_ = p.jr.AdvanceStage(job.ID, model.JobStage(res.NextStage))
	logger.Info("stage advanced", "next", res.NextStage)
}
