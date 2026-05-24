package repository

import (
	"errors"
	"time"

	"samqna/model"

	"gorm.io/gorm"
)

type JobRepo struct {
	DB *gorm.DB
}

func NewJobRepo(db *gorm.DB) *JobRepo {
	return &JobRepo{DB: db}
}

func (r *JobRepo) Enqueue(submissionID string, stage model.JobStage) error {
	j := &model.Job{
		SubmissionID: submissionID,
		Stage:        stage,
		Status:       model.JobPending,
		NextRunAt:    time.Now(),
	}
	return r.DB.Create(j).Error
}

// Claim atomically picks one pending job whose next_run_at <= now and marks it running.
// Returns nil, nil if no claimable job.
func (r *JobRepo) Claim(workerID string) (*model.Job, error) {
	var claimed *model.Job
	err := r.DB.Transaction(func(tx *gorm.DB) error {
		var j model.Job
		err := tx.Where("status = ? AND next_run_at <= ?", model.JobPending, time.Now()).
			Order("id ASC").First(&j).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		now := time.Now()
		j.Status = model.JobRunning
		j.LockedBy = &workerID
		j.LockedAt = &now
		if err := tx.Save(&j).Error; err != nil {
			return err
		}
		claimed = &j
		return nil
	})
	return claimed, err
}

func (r *JobRepo) AdvanceStage(jobID uint, next model.JobStage) error {
	updates := map[string]any{
		"stage":      next,
		"status":     model.JobPending,
		"attempts":   0,
		"last_error": nil,
		"locked_by":  nil,
		"locked_at":  nil,
		"next_run_at": time.Now(),
	}
	return r.DB.Model(&model.Job{}).Where("id = ?", jobID).Updates(updates).Error
}

func (r *JobRepo) Complete(jobID uint) error {
	return r.DB.Delete(&model.Job{}, jobID).Error
}

func (r *JobRepo) RecordFailure(jobID uint, errMsg string) error {
	var j model.Job
	if err := r.DB.First(&j, jobID).Error; err != nil {
		return err
	}
	j.Attempts++
	j.LastError = &errMsg
	j.Status = model.JobPending
	j.LockedBy = nil
	j.LockedAt = nil
	j.NextRunAt = time.Now().Add(backoff(j.Attempts))
	return r.DB.Save(&j).Error
}

// RecordFailureWithBackoff is like RecordFailure but uses the caller-supplied
// backoff instead of the package's default schedule. Intended for tests that
// need fast retries.
func (r *JobRepo) RecordFailureWithBackoff(jobID uint, errMsg string, after time.Duration) error {
	var j model.Job
	if err := r.DB.First(&j, jobID).Error; err != nil {
		return err
	}
	j.Attempts++
	j.LastError = &errMsg
	j.Status = model.JobPending
	j.LockedBy = nil
	j.LockedAt = nil
	j.NextRunAt = time.Now().Add(after)
	return r.DB.Save(&j).Error
}

func (r *JobRepo) MarkPermanentFailure(jobID uint, errMsg string) error {
	return r.DB.Model(&model.Job{}).Where("id = ?", jobID).
		Updates(map[string]any{"status": model.JobFailed, "last_error": errMsg}).Error
}

func (r *JobRepo) GetBySubmission(submissionID string) (*model.Job, error) {
	var j model.Job
	if err := r.DB.Where("submission_id = ?", submissionID).First(&j).Error; err != nil {
		return nil, err
	}
	return &j, nil
}

func (r *JobRepo) ReleaseStaleLocks(maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge)
	res := r.DB.Model(&model.Job{}).
		Where("status = ? AND locked_at < ?", model.JobRunning, cutoff).
		Updates(map[string]any{
			"status":    model.JobPending,
			"locked_by": nil,
			"locked_at": nil,
		})
	return res.RowsAffected, res.Error
}

func (r *JobRepo) QueueDepth() (int64, error) {
	var n int64
	err := r.DB.Model(&model.Job{}).Where("status = ?", model.JobPending).Count(&n).Error
	return n, err
}

// backoff: 30s, 2m, 10m, 1h, then 1h indefinitely.
func backoff(attempts int) time.Duration {
	switch attempts {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	case 3:
		return 10 * time.Minute
	default:
		return time.Hour
	}
}
