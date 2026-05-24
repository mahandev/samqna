package model

import "time"

type Job struct {
	ID           uint      `gorm:"primaryKey"`
	SubmissionID string    `gorm:"uniqueIndex;size:26"`
	Stage        JobStage  `gorm:"size:20"`
	Status       JobStatus `gorm:"size:10;index"`
	Attempts     int
	LastError    *string
	LockedBy     *string
	LockedAt     *time.Time
	NextRunAt    time.Time `gorm:"index"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
