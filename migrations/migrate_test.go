package migrations

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrate_CreatesAllTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, table := range []string{"submissions", "tags", "submission_tags", "jobs", "blocked_ips"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table %q missing after Migrate", table)
		}
	}
}
