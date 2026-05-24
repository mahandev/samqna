package model

import "time"

type BlockedIP struct {
	IP        string    `gorm:"primaryKey;size:64"`
	Reason    string
	BlockedAt time.Time
}
