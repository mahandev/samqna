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

	wg     sync.WaitGroup
	stop   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

func NewPool(db *gorm.DB, sr *repository.SubmissionRepo, jr *repository.JobRepo, reg *Registry, workers int, poll time.Duration, maxAttempts int) *Pool {
	return &Pool{
		db: db, sr: sr, jr: jr, reg: reg,
		workers: workers, pollEvery: poll, maxAttempts: maxAttempts,
		stop: make(chan struct{}),
	}
}

func (p *Pool) Start() {
	p.ctx, p.cancel = context.WithCancel(context.Background())
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.workerLoop(fmt.Sprintf("w%d", i))
	}
}

func (p *Pool) Stop(timeout time.Duration) {
	if p.cancel != nil {
		p.cancel()
	}
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

	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Minute)
	defer cancel()
	res := RunStage(ctx, stage, sub)

	if res.Err != nil {
		logger.Warn("stage failed", "err", res.Err)
		if job.Attempts+1 >= p.maxAttempts {
			if err := p.jr.MarkPermanentFailure(job.ID, res.Err.Error()); err != nil {
				logger.Error("mark permanent failure", "err", err)
			}
			sub.Status = model.StatusFailed
			if err := p.sr.Update(sub); err != nil {
				logger.Error("update submission to failed", "err", err)
			}
			logger.Error("permanent failure")
			return
		}
		if p.backoffOverride > 0 {
			if err := p.jr.RecordFailureWithBackoff(job.ID, res.Err.Error(), p.backoffOverride); err != nil {
				logger.Error("record failure (override)", "err", err)
			}
			return
		}
		if err := p.jr.RecordFailure(job.ID, res.Err.Error()); err != nil {
			logger.Error("record failure", "err", err)
		}
		return
	}
	if res.Terminal {
		if sub.Status != model.StatusQuarantined {
			sub.Status = model.StatusReady
			if err := p.sr.Update(sub); err != nil {
				logger.Error("update submission to ready", "err", err)
			}
		}
		if err := p.jr.Complete(job.ID); err != nil {
			logger.Error("complete job", "err", err)
		}
		logger.Info("submission ready")
		return
	}
	if err := p.sr.Update(sub); err != nil {
		logger.Error("persist submission between stages", "err", err)
	}
	if err := p.jr.AdvanceStage(job.ID, model.JobStage(res.NextStage)); err != nil {
		logger.Error("advance stage", "err", err)
	}
	logger.Info("stage advanced", "next", res.NextStage)
}
