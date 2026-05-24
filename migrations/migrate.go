package migrations

import (
	"samqna/model"

	"gorm.io/gorm"
)

func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.Submission{},
		&model.Tag{},
		&model.Job{},
		&model.BlockedIP{},
		&model.AdminAudit{},
		&model.Setting{},
	)
}
