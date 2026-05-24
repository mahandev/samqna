package repository

import (
	"time"

	"samqna/model"

	"gorm.io/gorm"
)

// SettingsRepo is a tiny key/value store on the settings table. Used for
// runtime toggles (e.g. the submissions-paused kill switch) that we want
// to persist across restarts without redeploying.
type SettingsRepo struct {
	DB *gorm.DB
}

func NewSettingsRepo(db *gorm.DB) *SettingsRepo {
	return &SettingsRepo{DB: db}
}

func (r *SettingsRepo) Get(key string) (string, error) {
	var s model.Setting
	err := r.DB.First(&s, "key = ?", key).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return s.Value, nil
}

func (r *SettingsRepo) Set(key, value string) error {
	s := model.Setting{Key: key, Value: value, UpdatedAt: time.Now()}
	// Upsert via GORM Save: if PK exists, update; else insert.
	return r.DB.Save(&s).Error
}

// Bool reads a setting and returns true iff value == "1" / "true".
func (r *SettingsRepo) Bool(key string) (bool, error) {
	v, err := r.Get(key)
	if err != nil {
		return false, err
	}
	return v == "1" || v == "true", nil
}

func (r *SettingsRepo) SetBool(key string, v bool) error {
	if v {
		return r.Set(key, "1")
	}
	return r.Set(key, "0")
}

// Well-known keys.
const KeySubmissionsPaused = "submissions_paused"
