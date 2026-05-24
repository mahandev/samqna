package repository

import (
	"time"

	"samqna/model"

	"gorm.io/gorm"
)

type ListFilter struct {
	Tags        []string
	MinScore    int
	StarredOnly bool
	Limit       int
	Offset      int
}

type SubmissionRepo struct {
	DB *gorm.DB
}

func NewSubmissionRepo(db *gorm.DB) *SubmissionRepo {
	return &SubmissionRepo{DB: db}
}

func (r *SubmissionRepo) Create(s *model.Submission) error {
	return r.DB.Create(s).Error
}

func (r *SubmissionRepo) Get(id string) (*model.Submission, error) {
	var s model.Submission
	if err := r.DB.Preload("Tags").First(&s, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SubmissionRepo) Update(s *model.Submission) error {
	return r.DB.Save(s).Error
}

func (r *SubmissionRepo) ListReady(f ListFilter) ([]model.Submission, error) {
	q := r.DB.Model(&model.Submission{}).
		Where("status = ?", model.StatusReady).
		Where("deleted_at IS NULL").
		Preload("Tags")
	if f.MinScore > 0 {
		q = q.Where("quality_score >= ?", f.MinScore)
	}
	if f.StarredOnly {
		q = q.Where("starred = ?", true)
	}
	if len(f.Tags) > 0 {
		q = q.Joins("JOIN submission_tags st ON st.submission_id = submissions.id").
			Joins("JOIN tags t ON t.id = st.tag_id").
			Where("t.name IN ?", f.Tags).
			Group("submissions.id").
			Having("COUNT(DISTINCT t.id) = ?", len(f.Tags))
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	q = q.Order("created_at DESC").Limit(limit).Offset(f.Offset)
	var out []model.Submission
	return out, q.Find(&out).Error
}

func (r *SubmissionRepo) ListQuarantined(limit, offset int) ([]model.Submission, error) {
	var out []model.Submission
	err := r.DB.Where("status = ? AND deleted_at IS NULL", model.StatusQuarantined).
		Order("created_at DESC").Limit(limit).Offset(offset).Find(&out).Error
	return out, err
}

func (r *SubmissionRepo) CountFromIPSince(ip string, since time.Time) (int64, error) {
	var n int64
	err := r.DB.Model(&model.Submission{}).
		Where("submitter_ip = ? AND created_at >= ?", ip, since).
		Count(&n).Error
	return n, err
}

func (r *SubmissionRepo) SoftDelete(id string) error {
	now := time.Now()
	return r.DB.Model(&model.Submission{}).
		Where("id = ?", id).Update("deleted_at", now).Error
}

func (r *SubmissionRepo) PruneCandidates(retentionDays int) ([]model.Submission, error) {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var out []model.Submission
	err := r.DB.Where("starred = false AND pruned_at IS NULL AND video_path IS NOT NULL AND created_at < ?", cutoff).
		Find(&out).Error
	return out, err
}

func (r *SubmissionRepo) MarkPruned(id string) error {
	now := time.Now()
	return r.DB.Model(&model.Submission{}).Where("id = ?", id).
		Updates(map[string]any{"video_path": nil, "pruned_at": now}).Error
}
