package model

import (
	"time"

	"github.com/oklog/ulid/v2"
)

type Submission struct {
	ID               string           `gorm:"primaryKey;size:26"`
	CreatedAt        time.Time        `gorm:"index"`
	UpdatedAt        time.Time
	SubmitterEmail   *string
	SubmitterIP      string           `gorm:"index"`
	OriginalFilename string
	DurationSec      int
	SizeBytes        int64
	VideoPath        *string          // nullable: cleared on prune
	AudioPath        string
	ThumbnailPath    string
	Transcript       *string
	QualityScore     *int
	Summary          *string
	IsSpam           bool
	SpamReason       *string
	Status           SubmissionStatus `gorm:"size:20;index:idx_status_created,priority:1"`
	Starred          bool             `gorm:"index:idx_starred_created,priority:1"`
	StarReason       *string
	PrunedAt         *time.Time
	DeletedAt        *time.Time       `gorm:"index"`

	Tags []Tag `gorm:"many2many:submission_tags;"`
}

func NewSubmissionID() string {
	return ulid.Make().String()
}
