package repository

import (
	"time"

	"samqna/model"

	"gorm.io/gorm"
)

type AuditRepo struct {
	DB *gorm.DB
}

func NewAuditRepo(db *gorm.DB) *AuditRepo {
	return &AuditRepo{DB: db}
}

// Write records one admin action. Best-effort: callers should log on
// failure but not abort their request just because the audit row failed.
func (r *AuditRepo) Write(actor, action, target, meta string) error {
	return r.DB.Create(&model.AdminAudit{
		TS:     time.Now(),
		Actor:  actor,
		Action: action,
		Target: target,
		Meta:   meta,
	}).Error
}

func (r *AuditRepo) Recent(limit int) ([]model.AdminAudit, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var out []model.AdminAudit
	err := r.DB.Order("ts DESC").Limit(limit).Find(&out).Error
	return out, err
}
