package model

import "time"

// AdminAudit is one row per successful destructive admin action.
type AdminAudit struct {
	ID     uint      `gorm:"primaryKey"`
	TS     time.Time `gorm:"index"`
	Actor  string    `gorm:"size:200;index"` // email from CF Access JWT, or "token" for script path
	Action string    `gorm:"size:40;index"`  // e.g. "star", "delete", "quarantine_on", "requeue", "block_ip", "unblock_ip", "tag_edit", "pause", "unpause"
	Target string    `gorm:"size:64;index"`  // submission id, IP, or ""
	Meta   string    // JSON blob with extra detail (before/after for tag edits, etc.)
}

// Setting is a one-key/one-value store. Used for the global "submissions
// paused" kill switch and any future runtime toggles.
type Setting struct {
	Key       string `gorm:"primaryKey;size:64"`
	Value     string
	UpdatedAt time.Time
}
