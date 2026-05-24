package model

import "time"

type Tag struct {
	ID        uint      `gorm:"primaryKey"`
	Name      string    `gorm:"uniqueIndex;size:64"`
	CreatedAt time.Time
}
