package config

import (
	"os"
	"testing"
)

func TestLoadFromEnv_AppliesDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_PATH", "/tmp/x.db")
	t.Setenv("ADMIN_TOKEN", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != "9000" {
		t.Errorf("Port default = %q, want %q", cfg.Port, "9000")
	}
	if cfg.WorkerCount != 2 {
		t.Errorf("WorkerCount default = %d, want 2", cfg.WorkerCount)
	}
	if cfg.RetentionDays != 30 {
		t.Errorf("RetentionDays default = %d, want 30", cfg.RetentionDays)
	}
}

func TestLoadFromEnv_RequiresAdminToken(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_PATH", "/tmp/x.db")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when ADMIN_TOKEN missing")
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"PORT", "DATABASE_PATH", "ADMIN_TOKEN", "WORKER_COUNT", "RETENTION_DAYS"} {
		if v, ok := os.LookupEnv(k); ok {
			key, val := k, v
			t.Cleanup(func() { os.Setenv(key, val) })
			os.Unsetenv(k)
		}
	}
}

func TestGetenvInt_FallsBackOnGarbage(t *testing.T) {
	clearEnv(t)
	t.Setenv("DATABASE_PATH", "/tmp/x.db")
	t.Setenv("ADMIN_TOKEN", "x")
	t.Setenv("WORKER_COUNT", "not-a-number")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.WorkerCount != 2 {
		t.Errorf("WorkerCount on garbage = %d, want 2 (default)", cfg.WorkerCount)
	}
}
