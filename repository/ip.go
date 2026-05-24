package repository

import (
	"time"

	"samqna/model"

	"gorm.io/gorm"
)

type IPRepo struct {
	DB *gorm.DB
}

func NewIPRepo(db *gorm.DB) *IPRepo {
	return &IPRepo{DB: db}
}

func (r *IPRepo) IsBlocked(ip string) (bool, error) {
	var n int64
	err := r.DB.Model(&model.BlockedIP{}).Where("ip = ?", ip).Count(&n).Error
	return n > 0, err
}

func (r *IPRepo) Block(ip, reason string) error {
	return r.DB.Create(&model.BlockedIP{IP: ip, Reason: reason, BlockedAt: time.Now()}).Error
}

func (r *IPRepo) List() ([]model.BlockedIP, error) {
	var out []model.BlockedIP
	return out, r.DB.Order("blocked_at DESC").Find(&out).Error
}

func (r *IPRepo) Unblock(ip string) error {
	return r.DB.Delete(&model.BlockedIP{}, "ip = ?", ip).Error
}
