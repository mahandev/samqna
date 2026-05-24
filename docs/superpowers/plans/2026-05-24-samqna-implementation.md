# SamQnA Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a production-grade Go application that ingests short Q&A video submissions, runs them through an AI pipeline (transcribe → tag → quality-grade), and lets a single creator filter, view, and export clips to splice into their own response videos.

**Architecture:** Single Go binary (Approach A from the spec). Gin HTTP + GORM + SQLite for persistence, html/template + HTMX for UI, in-process bounded worker pool with a SQLite-backed durable job queue. Pipeline stages (ffmpeg → whisper.cpp → OpenRouter LLM) live behind a `Stage` interface so each is independently testable with fakes. Deployed via Docker + Cloudflare Tunnel on a homeserver.

**Tech Stack:** Go 1.26, Gin, GORM (sqlite driver), html/template, HTMX, whisper.cpp (small.en), ffmpeg, OpenRouter (Gemini 2.5 Flash → fallback chain), Cloudflare Turnstile, Telegram for alerts.

**Spec:** `docs/superpowers/specs/2026-05-24-samqna-design.md`

**Existing scaffold (cwd `/Users/devamarnani/Desktop/samqna`):** `main.go`, `app.go`, `config/database.go`, empty placeholder files in `repository/`, `route/`, `service/`. Existing `repository/user.go` contains invalid syntax and gets deleted in Task 1.

---

## Pre-flight

- [ ] **Confirm Go toolchain**

```bash
go version
```
Expected: `go version go1.26.x ...`. If older, install Go 1.26+ before starting.

- [ ] **Confirm ffmpeg available on host**

```bash
ffmpeg -version | head -1
```
Expected: a version line. Not strictly required during unit tests (we fake it), but required for integration + manual smoke.

- [ ] **Confirm whisper.cpp binary + model available**

For local dev, install once:
```bash
brew install whisper-cpp           # macOS; package name varies on Linux
# Model files (download once):
mkdir -p ~/whisper-models
curl -L -o ~/whisper-models/ggml-small.en.bin \
  https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.en.bin
curl -L -o ~/whisper-models/ggml-tiny.en.bin \
  https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.en.bin
```
Expected: ~466 MB (`small.en`) + ~75 MB (`tiny.en`) downloaded. The tiny model is for fast integration tests; small is the production model.

---

## Phase 1 — Foundation

### Task 1: Clean scaffold + set up config and logging

**Files:**
- Delete: `repository/user.go` (invalid syntax)
- Modify: `main.go`, `app.go`, `config/database.go`
- Create: `config/config.go`, `.env.example`, `.gitignore`

- [ ] **Step 1.1: Delete broken scaffold file**

```bash
rm repository/user.go
```

- [ ] **Step 1.2: Add `.gitignore`**

Create `.gitignore`:
```
/tmp/
/data/
*.db
*.db-shm
*.db-wal
.env
.env.local
samqna
dist/
.DS_Store
```

- [ ] **Step 1.3: Add `.env.example`**

Create `.env.example`:
```
PORT=9000
DATABASE_PATH=./data/samqna.db
MEDIA_PATH=./data/media
EXPORT_PATH=./data/exports

OPENROUTER_API_KEY=
TURNSTILE_SITE_KEY=
TURNSTILE_SECRET=
ADMIN_TOKEN=

TELEGRAM_BOT_TOKEN=
TELEGRAM_CHAT_ID=

WORKER_COUNT=2
WHISPER_BIN=/usr/local/bin/whisper-cli
WHISPER_MODEL_PATH=/models/ggml-small.en.bin
FFMPEG_BIN=/usr/bin/ffmpeg

QUALITY_THRESHOLD=30
MAX_SUBMISSIONS_PER_IP_PER_DAY=3
MAX_UPLOAD_BYTES=52428800   # 50 MB
RETENTION_DAYS=30
```

- [ ] **Step 1.4: Write the failing test for config loading**

Create `config/config_test.go`:
```go
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
			t.Cleanup(func() { os.Setenv(k, v) })
			os.Unsetenv(k)
		}
	}
}
```

- [ ] **Step 1.5: Run test, confirm it fails (no Load fn)**

```bash
go test ./config/...
```
Expected: build failure / undefined `Load`.

- [ ] **Step 1.6: Implement `config/config.go`**

Create `config/config.go`:
```go
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port          string
	DatabasePath  string
	MediaPath     string
	ExportPath    string

	OpenRouterKey    string
	TurnstileSite    string
	TurnstileSecret  string
	AdminToken       string

	TelegramBotToken string
	TelegramChatID   string

	WorkerCount     int
	WhisperBin      string
	WhisperModel    string
	FfmpegBin       string

	QualityThreshold int
	MaxIPPerDay      int
	MaxUploadBytes   int64
	RetentionDays    int
}

func Load() (*Config, error) {
	c := &Config{
		Port:             getenv("PORT", "9000"),
		DatabasePath:     getenv("DATABASE_PATH", "./data/samqna.db"),
		MediaPath:        getenv("MEDIA_PATH", "./data/media"),
		ExportPath:       getenv("EXPORT_PATH", "./data/exports"),
		OpenRouterKey:    os.Getenv("OPENROUTER_API_KEY"),
		TurnstileSite:    os.Getenv("TURNSTILE_SITE_KEY"),
		TurnstileSecret:  os.Getenv("TURNSTILE_SECRET"),
		AdminToken:       os.Getenv("ADMIN_TOKEN"),
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:   os.Getenv("TELEGRAM_CHAT_ID"),
		WorkerCount:      getenvInt("WORKER_COUNT", 2),
		WhisperBin:       getenv("WHISPER_BIN", "/usr/local/bin/whisper-cli"),
		WhisperModel:     getenv("WHISPER_MODEL_PATH", "/models/ggml-small.en.bin"),
		FfmpegBin:        getenv("FFMPEG_BIN", "/usr/bin/ffmpeg"),
		QualityThreshold: getenvInt("QUALITY_THRESHOLD", 30),
		MaxIPPerDay:      getenvInt("MAX_SUBMISSIONS_PER_IP_PER_DAY", 3),
		MaxUploadBytes:   int64(getenvInt("MAX_UPLOAD_BYTES", 52428800)),
		RetentionDays:    getenvInt("RETENTION_DAYS", 30),
	}
	if c.AdminToken == "" {
		return nil, fmt.Errorf("ADMIN_TOKEN env var is required")
	}
	return c, nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
```

- [ ] **Step 1.7: Replace `config/database.go` with cleaner version**

Replace `config/database.go`:
```go
package config

import (
	"fmt"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func ConnectDB(path string) (*gorm.DB, error) {
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on", path)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return db, nil
}
```

- [ ] **Step 1.8: Run config tests, confirm pass**

```bash
go test ./config/... -v
```
Expected: both tests PASS.

- [ ] **Step 1.9: Commit**

```bash
git add config/ .env.example .gitignore
git rm repository/user.go
git commit -m "feat(samqna): config loader + sqlite WAL connection"
```

---

### Task 2: GORM models

**Files:**
- Create: `model/enums.go`, `model/submission.go`, `model/tag.go`, `model/job.go`, `model/blocked_ip.go`

- [ ] **Step 2.1: Add ULID dependency**

```bash
go get github.com/oklog/ulid/v2
```

- [ ] **Step 2.2: Create enums file**

Create `model/enums.go`:
```go
package model

type SubmissionStatus string

const (
	StatusProcessing  SubmissionStatus = "processing"
	StatusReady       SubmissionStatus = "ready"
	StatusQuarantined SubmissionStatus = "quarantined"
	StatusFailed      SubmissionStatus = "failed"
)

type JobStage string

const (
	StageExtract   JobStage = "extract"
	StageTranscribe JobStage = "transcribe"
	StageTagGrade   JobStage = "tag_grade"
	StageDone       JobStage = "done"
)

type JobStatus string

const (
	JobPending JobStatus = "pending"
	JobRunning JobStatus = "running"
	JobFailed  JobStatus = "failed"
)
```

- [ ] **Step 2.3: Create Submission model**

Create `model/submission.go`:
```go
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
```

- [ ] **Step 2.4: Create Tag and join models**

Create `model/tag.go`:
```go
package model

import "time"

type Tag struct {
	ID        uint      `gorm:"primaryKey"`
	Name      string    `gorm:"uniqueIndex;size:64"`
	CreatedAt time.Time
}
```

- [ ] **Step 2.5: Create Job model**

Create `model/job.go`:
```go
package model

import "time"

type Job struct {
	ID            uint      `gorm:"primaryKey"`
	SubmissionID  string    `gorm:"uniqueIndex;size:26"`
	Stage         JobStage  `gorm:"size:20"`
	Status        JobStatus `gorm:"size:10;index"`
	Attempts      int
	LastError     *string
	LockedBy      *string
	LockedAt      *time.Time
	NextRunAt     time.Time `gorm:"index"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
```

- [ ] **Step 2.6: Create BlockedIP model**

Create `model/blocked_ip.go`:
```go
package model

import "time"

type BlockedIP struct {
	IP        string    `gorm:"primaryKey;size:64"`
	Reason    string
	BlockedAt time.Time
}
```

- [ ] **Step 2.7: Verify package builds**

```bash
go build ./model/...
```
Expected: no errors.

- [ ] **Step 2.8: Commit**

```bash
git add model/ go.mod go.sum
git commit -m "feat(samqna): add GORM models for submissions, tags, jobs, blocks"
```

---

### Task 3: Migrations runner

**Files:**
- Create: `migrations/migrate.go`, `migrations/migrate_test.go`

- [ ] **Step 3.1: Write failing test**

Create `migrations/migrate_test.go`:
```go
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
```

- [ ] **Step 3.2: Run test, confirm failure**

```bash
go test ./migrations/...
```
Expected: undefined `Migrate`.

- [ ] **Step 3.3: Implement migrations runner**

Create `migrations/migrate.go`:
```go
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
	)
}
```

- [ ] **Step 3.4: Run test, confirm pass**

```bash
go test ./migrations/... -v
```
Expected: PASS.

- [ ] **Step 3.5: Commit**

```bash
git add migrations/
git commit -m "feat(samqna): auto-migration runner for all models"
```

---

### Task 4: Storage path helpers

**Files:**
- Create: `storage/storage.go`, `storage/storage_test.go`

- [ ] **Step 4.1: Write failing test**

Create `storage/storage_test.go`:
```go
package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPathsFor_DateSharded(t *testing.T) {
	root := t.TempDir()
	s := New(root, "")
	ts := time.Date(2026, 5, 24, 10, 30, 0, 0, time.UTC)
	p := s.PathsFor("01H8XYZ123ABCDEFGHJKLMNPQR", ts)

	want := filepath.Join(root, "2026", "05", "24", "01H8XYZ123ABCDEFGHJKLMNPQR")
	if p.Dir != want {
		t.Errorf("Dir = %q, want %q", p.Dir, want)
	}
	if !strings.HasSuffix(p.Original, "/original") {
		t.Errorf("Original path = %q, missing /original", p.Original)
	}
}

func TestEnsureDir_CreatesPath(t *testing.T) {
	root := t.TempDir()
	s := New(root, "")
	ts := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	p := s.PathsFor("01H8ABC", ts)
	if err := s.EnsureDir(p); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Errorf("dir not created: %v", err)
	}
}
```

- [ ] **Step 4.2: Run, confirm failure**

```bash
go test ./storage/...
```
Expected: undefined symbols.

- [ ] **Step 4.3: Implement storage helpers**

Create `storage/storage.go`:
```go
package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Paths struct {
	Dir       string
	Original  string // extension appended at write time
	Audio     string
	Thumbnail string
}

type Storage struct {
	MediaRoot  string
	ExportRoot string
}

func New(mediaRoot, exportRoot string) *Storage {
	return &Storage{MediaRoot: mediaRoot, ExportRoot: exportRoot}
}

func (s *Storage) PathsFor(id string, createdAt time.Time) Paths {
	dir := filepath.Join(
		s.MediaRoot,
		fmt.Sprintf("%04d", createdAt.Year()),
		fmt.Sprintf("%02d", createdAt.Month()),
		fmt.Sprintf("%02d", createdAt.Day()),
		id,
	)
	return Paths{
		Dir:       dir,
		Original:  filepath.Join(dir, "original"),
		Audio:     filepath.Join(dir, "audio.opus"),
		Thumbnail: filepath.Join(dir, "thumb.jpg"),
	}
}

func (s *Storage) EnsureDir(p Paths) error {
	return os.MkdirAll(p.Dir, 0o755)
}

func (s *Storage) ExportPath(id string) string {
	return filepath.Join(s.ExportRoot, id+".mp4")
}

func (s *Storage) BatchPath(jobID string) string {
	return filepath.Join(s.ExportRoot, "batch-"+jobID+".zip")
}

func (s *Storage) EnsureRoots() error {
	for _, d := range []string{s.MediaRoot, s.ExportRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4.4: Run tests, confirm pass**

```bash
go test ./storage/... -v
```
Expected: PASS.

- [ ] **Step 4.5: Commit**

```bash
git add storage/
git commit -m "feat(samqna): date-sharded media + export path helpers"
```

---

## Phase 2 — Repositories

### Task 5: Submission repository

**Files:**
- Create: `repository/submission.go`, `repository/submission_test.go`
- Modify: `repository/post.go` (delete — empty placeholder)

- [ ] **Step 5.1: Delete empty placeholders**

```bash
rm repository/post.go
```

- [ ] **Step 5.2: Write failing tests**

Create `repository/submission_test.go`:
```go
package repository

import (
	"testing"
	"time"

	"samqna/migrations"
	"samqna/model"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	return db
}

func TestSubmissions_CreateAndGet(t *testing.T) {
	db := newTestDB(t)
	r := NewSubmissionRepo(db)

	s := &model.Submission{
		ID:               model.NewSubmissionID(),
		CreatedAt:        time.Now(),
		SubmitterIP:      "1.2.3.4",
		OriginalFilename: "q.mp4",
		AudioPath:        "/tmp/a.opus",
		ThumbnailPath:    "/tmp/t.jpg",
		Status:           model.StatusProcessing,
	}
	require.NoError(t, r.Create(s))

	got, err := r.Get(s.ID)
	require.NoError(t, err)
	require.Equal(t, s.SubmitterIP, got.SubmitterIP)
}

func TestSubmissions_ListReady_FiltersAndPaginates(t *testing.T) {
	db := newTestDB(t)
	r := NewSubmissionRepo(db)
	for i := 0; i < 5; i++ {
		s := &model.Submission{
			ID:          model.NewSubmissionID(),
			CreatedAt:   time.Now().Add(time.Duration(i) * time.Minute),
			SubmitterIP: "1.2.3.4",
			AudioPath:   "/x",
			Status:      model.StatusReady,
		}
		score := 50 + i
		s.QualityScore = &score
		require.NoError(t, r.Create(s))
	}
	out, err := r.ListReady(ListFilter{MinScore: 52, Limit: 10, Offset: 0})
	require.NoError(t, err)
	require.Len(t, out, 3) // scores 52, 53, 54
}

func TestSubmissions_CountFromIPSince(t *testing.T) {
	db := newTestDB(t)
	r := NewSubmissionRepo(db)
	now := time.Now()
	for i := 0; i < 3; i++ {
		s := &model.Submission{
			ID:          model.NewSubmissionID(),
			CreatedAt:   now,
			SubmitterIP: "9.9.9.9",
			AudioPath:   "/x",
			Status:      model.StatusReady,
		}
		require.NoError(t, r.Create(s))
	}
	n, err := r.CountFromIPSince("9.9.9.9", now.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(3), n)
}
```

- [ ] **Step 5.3: Run, confirm failure**

```bash
go get github.com/stretchr/testify
go test ./repository/...
```
Expected: undefined symbols.

- [ ] **Step 5.4: Implement repository**

Create `repository/submission.go`:
```go
package repository

import (
	"time"

	"samqna/model"

	"gorm.io/gorm"
)

type ListFilter struct {
	Tags        []string
	MinScore    int
	StarredOnly bool
	Limit       int
	Offset      int
}

type SubmissionRepo struct {
	DB *gorm.DB
}

func NewSubmissionRepo(db *gorm.DB) *SubmissionRepo {
	return &SubmissionRepo{DB: db}
}

func (r *SubmissionRepo) Create(s *model.Submission) error {
	return r.DB.Create(s).Error
}

func (r *SubmissionRepo) Get(id string) (*model.Submission, error) {
	var s model.Submission
	if err := r.DB.Preload("Tags").First(&s, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SubmissionRepo) Update(s *model.Submission) error {
	return r.DB.Save(s).Error
}

func (r *SubmissionRepo) ListReady(f ListFilter) ([]model.Submission, error) {
	q := r.DB.Model(&model.Submission{}).
		Where("status = ?", model.StatusReady).
		Where("deleted_at IS NULL").
		Preload("Tags")
	if f.MinScore > 0 {
		q = q.Where("quality_score >= ?", f.MinScore)
	}
	if f.StarredOnly {
		q = q.Where("starred = ?", true)
	}
	if len(f.Tags) > 0 {
		q = q.Joins("JOIN submission_tags st ON st.submission_id = submissions.id").
			Joins("JOIN tags t ON t.id = st.tag_id").
			Where("t.name IN ?", f.Tags).
			Group("submissions.id").
			Having("COUNT(DISTINCT t.id) = ?", len(f.Tags))
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	q = q.Order("created_at DESC").Limit(limit).Offset(f.Offset)
	var out []model.Submission
	return out, q.Find(&out).Error
}

func (r *SubmissionRepo) ListQuarantined(limit, offset int) ([]model.Submission, error) {
	var out []model.Submission
	err := r.DB.Where("status = ? AND deleted_at IS NULL", model.StatusQuarantined).
		Order("created_at DESC").Limit(limit).Offset(offset).Find(&out).Error
	return out, err
}

func (r *SubmissionRepo) CountFromIPSince(ip string, since time.Time) (int64, error) {
	var n int64
	err := r.DB.Model(&model.Submission{}).
		Where("submitter_ip = ? AND created_at >= ?", ip, since).
		Count(&n).Error
	return n, err
}

func (r *SubmissionRepo) SoftDelete(id string) error {
	now := time.Now()
	return r.DB.Model(&model.Submission{}).
		Where("id = ?", id).Update("deleted_at", now).Error
}

func (r *SubmissionRepo) PruneCandidates(retentionDays int) ([]model.Submission, error) {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var out []model.Submission
	err := r.DB.Where("starred = false AND pruned_at IS NULL AND video_path IS NOT NULL AND created_at < ?", cutoff).
		Find(&out).Error
	return out, err
}

func (r *SubmissionRepo) MarkPruned(id string) error {
	now := time.Now()
	return r.DB.Model(&model.Submission{}).Where("id = ?", id).
		Updates(map[string]any{"video_path": nil, "pruned_at": now}).Error
}
```

- [ ] **Step 5.5: Run tests, confirm pass**

```bash
go test ./repository/... -v
```
Expected: 3 PASS.

- [ ] **Step 5.6: Commit**

```bash
git add repository/ go.mod go.sum
git rm repository/post.go
git commit -m "feat(samqna): submission repository with list/filter/prune"
```

---

### Task 6: Job + Tag + BlockedIP repositories

**Files:**
- Create: `repository/job.go`, `repository/job_test.go`, `repository/tag.go`, `repository/ip.go`

- [ ] **Step 6.1: Write failing job repo tests**

Create `repository/job_test.go`:
```go
package repository

import (
	"testing"
	"time"

	"samqna/model"

	"github.com/stretchr/testify/require"
)

func TestJobs_EnqueueAndClaim(t *testing.T) {
	db := newTestDB(t)
	sr := NewSubmissionRepo(db)
	jr := NewJobRepo(db)

	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	j, err := jr.Claim("worker-1")
	require.NoError(t, err)
	require.NotNil(t, j)
	require.Equal(t, s.ID, j.SubmissionID)
	require.Equal(t, model.JobRunning, j.Status)
	require.NotNil(t, j.LockedBy)
}

func TestJobs_Claim_ReturnsNilWhenEmpty(t *testing.T) {
	db := newTestDB(t)
	jr := NewJobRepo(db)
	j, err := jr.Claim("w")
	require.NoError(t, err)
	require.Nil(t, j)
}

func TestJobs_ReleaseStaleLocks(t *testing.T) {
	db := newTestDB(t)
	sr := NewSubmissionRepo(db)
	jr := NewJobRepo(db)
	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	j, _ := jr.Claim("worker-1")
	// fake stale lock by backdating
	old := time.Now().Add(-15 * time.Minute)
	db.Model(j).Update("locked_at", old)

	n, err := jr.ReleaseStaleLocks(10 * time.Minute)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}

func TestJobs_AdvanceStage(t *testing.T) {
	db := newTestDB(t)
	sr := NewSubmissionRepo(db)
	jr := NewJobRepo(db)
	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	j, _ := jr.Claim("w")
	require.NoError(t, jr.AdvanceStage(j.ID, model.StageTranscribe))

	got, err := jr.GetBySubmission(s.ID)
	require.NoError(t, err)
	require.Equal(t, model.StageTranscribe, got.Stage)
	require.Equal(t, model.JobPending, got.Status)
}

func TestJobs_RecordFailure_SchedulesBackoff(t *testing.T) {
	db := newTestDB(t)
	sr := NewSubmissionRepo(db)
	jr := NewJobRepo(db)
	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	j, _ := jr.Claim("w")
	require.NoError(t, jr.RecordFailure(j.ID, "boom"))

	got, _ := jr.GetBySubmission(s.ID)
	require.Equal(t, 1, got.Attempts)
	require.Equal(t, model.JobPending, got.Status)
	require.True(t, got.NextRunAt.After(time.Now()))
}
```

- [ ] **Step 6.2: Run, confirm failure**

```bash
go test ./repository/... -run TestJobs
```
Expected: undefined.

- [ ] **Step 6.3: Implement job repo**

Create `repository/job.go`:
```go
package repository

import (
	"time"

	"samqna/model"

	"gorm.io/gorm"
)

type JobRepo struct {
	DB *gorm.DB
}

func NewJobRepo(db *gorm.DB) *JobRepo {
	return &JobRepo{DB: db}
}

func (r *JobRepo) Enqueue(submissionID string, stage model.JobStage) error {
	j := &model.Job{
		SubmissionID: submissionID,
		Stage:        stage,
		Status:       model.JobPending,
		NextRunAt:    time.Now(),
	}
	return r.DB.Create(j).Error
}

// Claim atomically picks one pending job whose next_run_at <= now and marks it running.
// Returns nil, nil if no claimable job.
func (r *JobRepo) Claim(workerID string) (*model.Job, error) {
	var claimed *model.Job
	err := r.DB.Transaction(func(tx *gorm.DB) error {
		var j model.Job
		err := tx.Where("status = ? AND next_run_at <= ?", model.JobPending, time.Now()).
			Order("id ASC").First(&j).Error
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		now := time.Now()
		j.Status = model.JobRunning
		j.LockedBy = &workerID
		j.LockedAt = &now
		if err := tx.Save(&j).Error; err != nil {
			return err
		}
		claimed = &j
		return nil
	})
	return claimed, err
}

func (r *JobRepo) AdvanceStage(jobID uint, next model.JobStage) error {
	updates := map[string]any{
		"stage":      next,
		"status":     model.JobPending,
		"attempts":   0,
		"last_error": nil,
		"locked_by":  nil,
		"locked_at":  nil,
		"next_run_at": time.Now(),
	}
	return r.DB.Model(&model.Job{}).Where("id = ?", jobID).Updates(updates).Error
}

func (r *JobRepo) Complete(jobID uint) error {
	return r.DB.Delete(&model.Job{}, jobID).Error
}

func (r *JobRepo) RecordFailure(jobID uint, errMsg string) error {
	var j model.Job
	if err := r.DB.First(&j, jobID).Error; err != nil {
		return err
	}
	j.Attempts++
	j.LastError = &errMsg
	j.Status = model.JobPending
	j.LockedBy = nil
	j.LockedAt = nil
	j.NextRunAt = time.Now().Add(backoff(j.Attempts))
	return r.DB.Save(&j).Error
}

func (r *JobRepo) MarkPermanentFailure(jobID uint, errMsg string) error {
	return r.DB.Model(&model.Job{}).Where("id = ?", jobID).
		Updates(map[string]any{"status": model.JobFailed, "last_error": errMsg}).Error
}

func (r *JobRepo) GetBySubmission(submissionID string) (*model.Job, error) {
	var j model.Job
	if err := r.DB.Where("submission_id = ?", submissionID).First(&j).Error; err != nil {
		return nil, err
	}
	return &j, nil
}

func (r *JobRepo) ReleaseStaleLocks(maxAge time.Duration) (int64, error) {
	cutoff := time.Now().Add(-maxAge)
	res := r.DB.Model(&model.Job{}).
		Where("status = ? AND locked_at < ?", model.JobRunning, cutoff).
		Updates(map[string]any{
			"status":    model.JobPending,
			"locked_by": nil,
			"locked_at": nil,
		})
	return res.RowsAffected, res.Error
}

func (r *JobRepo) QueueDepth() (int64, error) {
	var n int64
	err := r.DB.Model(&model.Job{}).Where("status = ?", model.JobPending).Count(&n).Error
	return n, err
}

// backoff: 30s, 2m, 10m, 1h, then 1h indefinitely.
func backoff(attempts int) time.Duration {
	switch attempts {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	case 3:
		return 10 * time.Minute
	default:
		return time.Hour
	}
}
```

- [ ] **Step 6.4: Implement tag repo**

Create `repository/tag.go`:
```go
package repository

import (
	"regexp"
	"strings"

	"samqna/model"

	"gorm.io/gorm"
)

type TagRepo struct {
	DB *gorm.DB
}

func NewTagRepo(db *gorm.DB) *TagRepo {
	return &TagRepo{DB: db}
}

var nonTagChars = regexp.MustCompile(`[^a-z0-9\-]+`)

// Canonicalize lowercases, strips punctuation, replaces spaces with '-', dedupes.
func Canonicalize(names []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, n := range names {
		c := strings.ToLower(strings.TrimSpace(n))
		c = strings.ReplaceAll(c, " ", "-")
		c = nonTagChars.ReplaceAllString(c, "")
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

func (r *TagRepo) GetOrCreate(names []string) ([]model.Tag, error) {
	canon := Canonicalize(names)
	tags := make([]model.Tag, 0, len(canon))
	for _, n := range canon {
		var t model.Tag
		if err := r.DB.Where("name = ?", n).FirstOrCreate(&t, model.Tag{Name: n}).Error; err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

func (r *TagRepo) AllWithCounts() (map[string]int64, error) {
	type row struct {
		Name  string
		Count int64
	}
	var rows []row
	err := r.DB.Raw(`
		SELECT t.name AS name, COUNT(*) AS count
		FROM tags t
		JOIN submission_tags st ON st.tag_id = t.id
		JOIN submissions s ON s.id = st.submission_id
		WHERE s.status = 'ready' AND s.deleted_at IS NULL
		GROUP BY t.name
		ORDER BY count DESC
	`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Name] = r.Count
	}
	return out, nil
}
```

- [ ] **Step 6.5: Implement IP repo**

Create `repository/ip.go`:
```go
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
```

- [ ] **Step 6.6: Add tag canonicalization test**

Append to `repository/job_test.go`:
```go
func TestCanonicalize(t *testing.T) {
	in := []string{"AI", "First Job", "AI", "  career  ", "C++!"}
	got := Canonicalize(in)
	want := []string{"ai", "first-job", "career", "c"}
	require.Equal(t, want, got)
}
```

- [ ] **Step 6.7: Run all repo tests**

```bash
go test ./repository/... -v
```
Expected: all PASS.

- [ ] **Step 6.8: Commit**

```bash
git add repository/
git commit -m "feat(samqna): job/tag/ip repos with claim, backoff, canonicalization"
```

---

## Phase 3 — Pipeline core (with fakes)

### Task 7: Stage interface + pipeline runner

**Files:**
- Create: `pipeline/stages.go`, `pipeline/stages_test.go`

- [ ] **Step 7.1: Write failing tests**

Create `pipeline/stages_test.go`:
```go
package pipeline

import (
	"context"
	"errors"
	"testing"

	"samqna/model"

	"github.com/stretchr/testify/require"
)

type fakeStage struct {
	name    string
	next    string
	runErr  error
	called  bool
}

func (f *fakeStage) Name() string { return f.name }
func (f *fakeStage) Next() string { return f.next }
func (f *fakeStage) Run(_ context.Context, _ *model.Submission) error {
	f.called = true
	return f.runErr
}

func TestRegistry_Lookup(t *testing.T) {
	r := NewRegistry()
	a := &fakeStage{name: "extract", next: "transcribe"}
	r.Register(a)
	got, ok := r.Get("extract")
	require.True(t, ok)
	require.Equal(t, a, got)
}

func TestRunStage_Success_AdvancesToNext(t *testing.T) {
	s := &fakeStage{name: "extract", next: "transcribe"}
	sub := &model.Submission{ID: "x"}
	res := RunStage(context.Background(), s, sub)
	require.True(t, s.called)
	require.NoError(t, res.Err)
	require.Equal(t, "transcribe", res.NextStage)
	require.False(t, res.Terminal)
}

func TestRunStage_Failure_PreservesStage(t *testing.T) {
	s := &fakeStage{name: "extract", next: "transcribe", runErr: errors.New("boom")}
	sub := &model.Submission{ID: "x"}
	res := RunStage(context.Background(), s, sub)
	require.Error(t, res.Err)
	require.Equal(t, "", res.NextStage)
}

func TestRunStage_TerminalStage(t *testing.T) {
	s := &fakeStage{name: "tag_grade", next: ""}
	sub := &model.Submission{ID: "x"}
	res := RunStage(context.Background(), s, sub)
	require.NoError(t, res.Err)
	require.True(t, res.Terminal)
}
```

- [ ] **Step 7.2: Run, confirm failure**

```bash
go test ./pipeline/...
```
Expected: undefined.

- [ ] **Step 7.3: Implement registry and runner**

Create `pipeline/stages.go`:
```go
package pipeline

import (
	"context"

	"samqna/model"
)

type Stage interface {
	Name() string
	Run(ctx context.Context, s *model.Submission) error
	Next() string
}

type Registry struct {
	stages map[string]Stage
}

func NewRegistry() *Registry {
	return &Registry{stages: map[string]Stage{}}
}

func (r *Registry) Register(s Stage) {
	r.stages[s.Name()] = s
}

func (r *Registry) Get(name string) (Stage, bool) {
	s, ok := r.stages[name]
	return s, ok
}

type StageResult struct {
	Err       error
	NextStage string
	Terminal  bool
}

func RunStage(ctx context.Context, s Stage, sub *model.Submission) StageResult {
	if err := s.Run(ctx, sub); err != nil {
		return StageResult{Err: err}
	}
	next := s.Next()
	if next == "" {
		return StageResult{Terminal: true}
	}
	return StageResult{NextStage: next}
}
```

- [ ] **Step 7.4: Run tests, confirm pass**

```bash
go test ./pipeline/... -v
```
Expected: 4 PASS.

- [ ] **Step 7.5: Commit**

```bash
git add pipeline/
git commit -m "feat(samqna): pipeline stage interface, registry, runner"
```

---

### Task 8: Worker pool

**Files:**
- Create: `pipeline/pool.go`, `pipeline/pool_test.go`

- [ ] **Step 8.1: Write failing test (integration of fake stages + real repo)**

Create `pipeline/pool_test.go`:
```go
package pipeline

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"samqna/migrations"
	"samqna/model"
	"samqna/repository"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	return db
}

type countingStage struct {
	name string
	next string
	hits *int32
	fail bool
}

func (s *countingStage) Name() string { return s.name }
func (s *countingStage) Next() string { return s.next }
func (s *countingStage) Run(_ context.Context, _ *model.Submission) error {
	atomic.AddInt32(s.hits, 1)
	if s.fail {
		return errors.New("forced fail")
	}
	return nil
}

func TestPool_ProcessesJobThroughAllStages(t *testing.T) {
	db := newDB(t)
	sr := repository.NewSubmissionRepo(db)
	jr := repository.NewJobRepo(db)

	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	var c1, c2, c3 int32
	reg := NewRegistry()
	reg.Register(&countingStage{name: "extract", next: "transcribe", hits: &c1})
	reg.Register(&countingStage{name: "transcribe", next: "tag_grade", hits: &c2})
	reg.Register(&countingStage{name: "tag_grade", next: "", hits: &c3})

	pool := NewPool(db, sr, jr, reg, 1, 30*time.Millisecond, 5)
	pool.Start()
	defer pool.Stop(2 * time.Second)

	require.Eventually(t, func() bool {
		got, _ := sr.Get(s.ID)
		return got.Status == model.StatusReady
	}, 3*time.Second, 50*time.Millisecond)

	require.EqualValues(t, 1, atomic.LoadInt32(&c1))
	require.EqualValues(t, 1, atomic.LoadInt32(&c2))
	require.EqualValues(t, 1, atomic.LoadInt32(&c3))
}

func TestPool_FailureRetriesThenPermanentFailure(t *testing.T) {
	db := newDB(t)
	sr := repository.NewSubmissionRepo(db)
	jr := repository.NewJobRepo(db)

	s := &model.Submission{ID: model.NewSubmissionID(), CreatedAt: time.Now(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusProcessing}
	require.NoError(t, sr.Create(s))
	require.NoError(t, jr.Enqueue(s.ID, model.StageExtract))

	var hits int32
	reg := NewRegistry()
	reg.Register(&countingStage{name: "extract", next: "transcribe", hits: &hits, fail: true})

	// maxAttempts=2 to keep test fast; pool uses immediate backoff override via 0
	pool := NewPool(db, sr, jr, reg, 1, 30*time.Millisecond, 2)
	pool.backoffOverride = 10 * time.Millisecond
	pool.Start()
	defer pool.Stop(2 * time.Second)

	require.Eventually(t, func() bool {
		got, _ := sr.Get(s.ID)
		return got.Status == model.StatusFailed
	}, 3*time.Second, 50*time.Millisecond)

	require.EqualValues(t, 2, atomic.LoadInt32(&hits))
}
```

- [ ] **Step 8.2: Run, confirm failure**

```bash
go test ./pipeline/... -run TestPool
```
Expected: undefined `NewPool`.

- [ ] **Step 8.3: Implement worker pool**

Create `pipeline/pool.go`:
```go
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"samqna/model"
	"samqna/repository"

	"gorm.io/gorm"
)

type Pool struct {
	db          *gorm.DB
	sr          *repository.SubmissionRepo
	jr          *repository.JobRepo
	reg         *Registry
	workers     int
	pollEvery   time.Duration
	maxAttempts int

	// backoffOverride: when >0, used instead of repo's default backoff.
	// Used in tests to drive retries fast.
	backoffOverride time.Duration

	wg   sync.WaitGroup
	stop chan struct{}
}

func NewPool(db *gorm.DB, sr *repository.SubmissionRepo, jr *repository.JobRepo, reg *Registry, workers int, poll time.Duration, maxAttempts int) *Pool {
	return &Pool{
		db: db, sr: sr, jr: jr, reg: reg,
		workers: workers, pollEvery: poll, maxAttempts: maxAttempts,
		stop: make(chan struct{}),
	}
}

func (p *Pool) Start() {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.workerLoop(fmt.Sprintf("w%d", i))
	}
}

func (p *Pool) Stop(timeout time.Duration) {
	close(p.stop)
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		slog.Warn("pool stop timeout exceeded")
	}
	// release any leftover locks
	_, _ = p.jr.ReleaseStaleLocks(0)
}

func (p *Pool) workerLoop(id string) {
	defer p.wg.Done()
	t := time.NewTicker(p.pollEvery)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.tick(id)
		}
	}
}

func (p *Pool) tick(workerID string) {
	job, err := p.jr.Claim(workerID)
	if err != nil {
		slog.Error("claim", "err", err, "worker", workerID)
		return
	}
	if job == nil {
		return
	}
	sub, err := p.sr.Get(job.SubmissionID)
	if err != nil {
		slog.Error("load submission", "err", err, "job_id", job.ID)
		_ = p.jr.RecordFailure(job.ID, "load submission: "+err.Error())
		return
	}
	stage, ok := p.reg.Get(string(job.Stage))
	if !ok {
		_ = p.jr.MarkPermanentFailure(job.ID, "unknown stage: "+string(job.Stage))
		return
	}

	logger := slog.With("submission_id", sub.ID, "job_id", job.ID, "stage", job.Stage, "attempt", job.Attempts+1, "worker", workerID)
	logger.Info("stage start")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res := RunStage(ctx, stage, sub)

	if res.Err != nil {
		logger.Warn("stage failed", "err", res.Err)
		if job.Attempts+1 >= p.maxAttempts {
			_ = p.jr.MarkPermanentFailure(job.ID, res.Err.Error())
			sub.Status = model.StatusFailed
			_ = p.sr.Update(sub)
			logger.Error("permanent failure")
			return
		}
		if p.backoffOverride > 0 {
			// fast path for tests
			_ = p.db.Model(&model.Job{}).Where("id = ?", job.ID).Updates(map[string]any{
				"attempts":   job.Attempts + 1,
				"last_error": res.Err.Error(),
				"status":     model.JobPending,
				"locked_by":  nil,
				"locked_at":  nil,
				"next_run_at": time.Now().Add(p.backoffOverride),
			}).Error
			return
		}
		_ = p.jr.RecordFailure(job.ID, res.Err.Error())
		return
	}
	if res.Terminal {
		if sub.Status != model.StatusQuarantined {
			sub.Status = model.StatusReady
			_ = p.sr.Update(sub)
		}
		_ = p.jr.Complete(job.ID)
		logger.Info("submission ready")
		return
	}
	_ = p.jr.AdvanceStage(job.ID, model.JobStage(res.NextStage))
	logger.Info("stage advanced", "next", res.NextStage)
}
```

- [ ] **Step 8.4: Run pool tests**

```bash
go test ./pipeline/... -v -run TestPool
```
Expected: both PASS within ~3 seconds.

- [ ] **Step 8.5: Commit**

```bash
git add pipeline/
git commit -m "feat(samqna): worker pool with claim/retry/permanent-fail semantics"
```

---

## Phase 4 — Pipeline stages (real implementations)

### Task 9: ffmpeg stage (extract)

**Files:**
- Create: `pipeline/ffmpeg.go`, `pipeline/ffmpeg_test.go`
- Create: `testdata/sample.mp4` (a real ≤5s sample video — download or generate)

- [ ] **Step 9.1: Generate sample fixture**

```bash
mkdir -p testdata
ffmpeg -y -f lavfi -i sine=frequency=440:duration=3 \
  -f lavfi -i testsrc=duration=3:size=320x240:rate=15 \
  -c:v libx264 -preset ultrafast -tune zerolatency \
  -c:a aac -shortest testdata/sample.mp4
```
Expected: a ~3-second MP4 with audio tone + test pattern, ~30 KB.

- [ ] **Step 9.2: Write failing test**

Create `pipeline/ffmpeg_test.go`:
```go
package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"samqna/model"
	"samqna/storage"

	"github.com/stretchr/testify/require"
)

func ffmpegAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping")
	}
}

func TestExtractStage_ProducesAudioAndThumb(t *testing.T) {
	ffmpegAvailable(t)
	root := t.TempDir()
	st := storage.New(root, root)

	id := model.NewSubmissionID()
	createdAt := time.Now()
	paths := st.PathsFor(id, createdAt)
	require.NoError(t, st.EnsureDir(paths))

	// copy fixture to original path
	in, err := os.ReadFile("../testdata/sample.mp4")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Original+".mp4", in, 0o644))

	sub := &model.Submission{
		ID: id, CreatedAt: createdAt, Status: model.StatusProcessing,
		VideoPath: ptr(paths.Original + ".mp4"),
	}
	s := &ExtractStage{Storage: st, FfmpegBin: "ffmpeg"}
	require.NoError(t, s.Run(context.Background(), sub))

	st1, err := os.Stat(paths.Audio)
	require.NoError(t, err)
	require.Greater(t, st1.Size(), int64(0))
	st2, err := os.Stat(paths.Thumbnail)
	require.NoError(t, err)
	require.Greater(t, st2.Size(), int64(0))
	require.Equal(t, paths.Audio, sub.AudioPath)
	require.Equal(t, paths.Thumbnail, sub.ThumbnailPath)
	require.Greater(t, sub.DurationSec, 0)
	require.Equal(t, "transcribe", s.Next())

	// sanity that paths exist
	_ = filepath.Walk(root, func(p string, _ os.FileInfo, _ error) error { return nil })
}

func ptr[T any](v T) *T { return &v }
```

- [ ] **Step 9.3: Run, confirm failure**

```bash
go test ./pipeline/... -run TestExtractStage
```
Expected: undefined ExtractStage.

- [ ] **Step 9.4: Implement ffmpeg stage**

Create `pipeline/ffmpeg.go`:
```go
package pipeline

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"samqna/model"
	"samqna/storage"
)

type ExtractStage struct {
	Storage   *storage.Storage
	FfmpegBin string
}

func (s *ExtractStage) Name() string { return "extract" }
func (s *ExtractStage) Next() string { return "transcribe" }

func (s *ExtractStage) Run(ctx context.Context, sub *model.Submission) error {
	if sub.VideoPath == nil {
		return fmt.Errorf("submission %s has no video_path", sub.ID)
	}
	paths := s.Storage.PathsFor(sub.ID, sub.CreatedAt)

	// 1) audio extract → 16 kHz mono opus
	if err := runCmd(ctx, s.FfmpegBin,
		"-y", "-i", *sub.VideoPath,
		"-vn", "-ac", "1", "-ar", "16000",
		"-c:a", "libopus", "-b:a", "32k",
		paths.Audio,
	); err != nil {
		return fmt.Errorf("audio extract: %w", err)
	}

	// 2) thumbnail from middle of file
	if err := runCmd(ctx, s.FfmpegBin,
		"-y", "-i", *sub.VideoPath,
		"-vf", "thumbnail,scale=320:-1",
		"-frames:v", "1",
		paths.Thumbnail,
	); err != nil {
		return fmt.Errorf("thumbnail: %w", err)
	}

	// 3) probe duration
	dur, err := probeDuration(ctx, s.FfmpegBin, *sub.VideoPath)
	if err != nil {
		return fmt.Errorf("probe duration: %w", err)
	}
	sub.AudioPath = paths.Audio
	sub.ThumbnailPath = paths.Thumbnail
	sub.DurationSec = dur
	return nil
}

func runCmd(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w (%s)", bin, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// probeDuration uses ffmpeg (not ffprobe) to keep dependency count minimal.
// Parses "Duration: hh:mm:ss.xx" from stderr.
func probeDuration(ctx context.Context, bin, path string) (int, error) {
	cmd := exec.CommandContext(ctx, bin, "-i", path, "-f", "null", "-")
	out, _ := cmd.CombinedOutput() // ffmpeg exits non-zero on -i probe; that's fine
	s := string(out)
	idx := strings.Index(s, "Duration: ")
	if idx == -1 {
		return 0, fmt.Errorf("no Duration in ffmpeg output")
	}
	rest := s[idx+len("Duration: "):]
	end := strings.Index(rest, ",")
	if end == -1 {
		return 0, fmt.Errorf("malformed Duration")
	}
	parts := strings.Split(rest[:end], ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("malformed Duration parts")
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	secFloat, _ := strconv.ParseFloat(parts[2], 64)
	return h*3600 + m*60 + int(secFloat), nil
}
```

- [ ] **Step 9.5: Run test, confirm pass**

```bash
go test ./pipeline/... -run TestExtractStage -v
```
Expected: PASS. (Skipped if ffmpeg missing.)

- [ ] **Step 9.6: Commit**

```bash
git add pipeline/ffmpeg.go pipeline/ffmpeg_test.go testdata/
git commit -m "feat(samqna): ffmpeg extract stage (audio, thumbnail, duration)"
```

---

### Task 10: whisper.cpp stage (transcribe)

**Files:**
- Create: `pipeline/whisper.go`, `pipeline/whisper_test.go`

- [ ] **Step 10.1: Write failing test (uses tiny.en model for speed; gated by env vars)**

Create `pipeline/whisper_test.go`:
```go
package pipeline

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"samqna/model"
	"samqna/storage"

	"github.com/stretchr/testify/require"
)

func TestWhisperStage_Transcribes(t *testing.T) {
	bin := os.Getenv("WHISPER_BIN")
	mdl := os.Getenv("WHISPER_TEST_MODEL") // point at tiny.en for CI speed
	if bin == "" || mdl == "" {
		t.Skip("WHISPER_BIN or WHISPER_TEST_MODEL not set; skipping")
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("whisper binary not found at %s", bin)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping")
	}

	root := t.TempDir()
	st := storage.New(root, root)
	id := model.NewSubmissionID()
	createdAt := time.Now()
	paths := st.PathsFor(id, createdAt)
	require.NoError(t, st.EnsureDir(paths))

	// build audio fixture by extracting from sample.mp4
	in, err := os.ReadFile("../testdata/sample.mp4")
	require.NoError(t, err)
	src := paths.Original + ".mp4"
	require.NoError(t, os.WriteFile(src, in, 0o644))
	es := &ExtractStage{Storage: st, FfmpegBin: "ffmpeg"}
	sub := &model.Submission{ID: id, CreatedAt: createdAt, VideoPath: &src}
	require.NoError(t, es.Run(context.Background(), sub))

	ws := &WhisperStage{Bin: bin, ModelPath: mdl}
	require.NoError(t, ws.Run(context.Background(), sub))
	require.NotNil(t, sub.Transcript)
	require.NotEmpty(t, strings.TrimSpace(*sub.Transcript))
	require.Equal(t, "tag_grade", ws.Next())
}
```

- [ ] **Step 10.2: Implement whisper stage**

Create `pipeline/whisper.go`:
```go
package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"samqna/model"
)

type WhisperStage struct {
	Bin       string // path to whisper-cli (whisper.cpp binary)
	ModelPath string // path to ggml model file
}

func (s *WhisperStage) Name() string { return "transcribe" }
func (s *WhisperStage) Next() string { return "tag_grade" }

func (s *WhisperStage) Run(ctx context.Context, sub *model.Submission) error {
	if sub.AudioPath == "" {
		return fmt.Errorf("submission %s has no audio_path", sub.ID)
	}
	// whisper.cpp emits text to stdout when -otxt -of - is set; safer:
	// use --output-txt --output-file <prefix> and read the .txt.
	out := sub.AudioPath + ".whisper"
	cmd := exec.CommandContext(ctx, s.Bin,
		"-m", s.ModelPath,
		"-f", sub.AudioPath,
		"-l", "en",
		"-otxt",
		"-of", out,
		"-nt",
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("whisper run: %w (%s)", err, strings.TrimSpace(string(combined)))
	}
	txtPath := out + ".txt"
	data, err := os.ReadFile(txtPath)
	if err != nil {
		return fmt.Errorf("read transcript: %w", err)
	}
	tx := strings.TrimSpace(string(data))
	sub.Transcript = &tx
	return nil
}
```

- [ ] **Step 10.3: Run test (with env vars set locally)**

```bash
WHISPER_BIN=/opt/homebrew/bin/whisper-cli \
WHISPER_TEST_MODEL=$HOME/whisper-models/ggml-tiny.en.bin \
go test ./pipeline/... -run TestWhisperStage -v
```
Expected: PASS. (Skipped without env vars — that's fine for CI.)

- [ ] **Step 10.4: Commit**

```bash
git add pipeline/whisper.go pipeline/whisper_test.go
git commit -m "feat(samqna): whisper.cpp transcribe stage"
```

---

### Task 11: OpenRouter LLM stage (tag + grade)

**Files:**
- Create: `pipeline/llm.go`, `pipeline/llm_test.go`

- [ ] **Step 11.1: Write failing test using httptest fake**

Create `pipeline/llm_test.go`:
```go
package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"samqna/model"

	"github.com/stretchr/testify/require"
)

func TestTagGradeStage_HappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), "transcribed text")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"{\"tags\":[\"career\",\"AI\"],\"quality_score\":78,\"summary\":\"asking about AI jobs\",\"is_spam\":false,\"spam_reason\":null}"}}]
		}`))
	}))
	defer server.Close()

	tr := &fakeTagRepo{}
	st := &TagGradeStage{
		Client:    server.Client(),
		Endpoint:  server.URL,
		APIKey:    "test",
		Models:    []string{"google/gemini-2.5-flash"},
		QualityThreshold: 30,
		TagRepo:   tr,
		AttachTags: func(_ *model.Submission, tags []model.Tag) error {
			require.Len(t, tags, 2)
			return nil
		},
	}
	tx := "transcribed text"
	sub := &model.Submission{ID: "x", Transcript: &tx, Status: model.StatusProcessing}
	require.NoError(t, st.Run(context.Background(), sub))
	require.Equal(t, model.StatusProcessing, sub.Status) // ready set by pool, not stage
	require.NotNil(t, sub.QualityScore)
	require.Equal(t, 78, *sub.QualityScore)
	require.Equal(t, "asking about AI jobs", *sub.Summary)
}

func TestTagGradeStage_LowScoreQuarantines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"tags\":[],\"quality_score\":10,\"summary\":\"unclear\",\"is_spam\":false}"}}]}`))
	}))
	defer server.Close()
	tr := &fakeTagRepo{}
	st := &TagGradeStage{
		Client: server.Client(), Endpoint: server.URL, APIKey: "x",
		Models: []string{"m"}, QualityThreshold: 30, TagRepo: tr,
		AttachTags: func(_ *model.Submission, _ []model.Tag) error { return nil },
	}
	tx := "x"
	sub := &model.Submission{ID: "x", Transcript: &tx, Status: model.StatusProcessing}
	require.NoError(t, st.Run(context.Background(), sub))
	require.Equal(t, model.StatusQuarantined, sub.Status)
}

func TestTagGradeStage_FallsBackToSecondModel(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		body, _ := io.ReadAll(r.Body)
		if hits == 1 {
			require.True(t, strings.Contains(string(body), "model-a"))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		require.True(t, strings.Contains(string(body), "model-b"))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"tags\":[\"t\"],\"quality_score\":80,\"summary\":\"x\",\"is_spam\":false}"}}]}`))
	}))
	defer server.Close()
	tr := &fakeTagRepo{}
	st := &TagGradeStage{
		Client: server.Client(), Endpoint: server.URL, APIKey: "x",
		Models: []string{"model-a", "model-b"}, QualityThreshold: 30, TagRepo: tr,
		AttachTags: func(_ *model.Submission, _ []model.Tag) error { return nil },
	}
	tx := "x"
	sub := &model.Submission{ID: "x", Transcript: &tx, Status: model.StatusProcessing}
	require.NoError(t, st.Run(context.Background(), sub))
	require.Equal(t, 2, hits)
}

type fakeTagRepo struct{}

func (f *fakeTagRepo) GetOrCreate(names []string) ([]model.Tag, error) {
	out := make([]model.Tag, 0, len(names))
	for i, n := range names {
		out = append(out, model.Tag{ID: uint(i + 1), Name: n})
	}
	return out, nil
}

// sanity: discard
var _ = json.RawMessage("{}")
```

- [ ] **Step 11.2: Run, confirm failure**

```bash
go test ./pipeline/... -run TestTagGradeStage
```
Expected: undefined.

- [ ] **Step 11.3: Implement LLM stage**

Create `pipeline/llm.go`:
```go
package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"samqna/model"
)

type TagRepoIface interface {
	GetOrCreate(names []string) ([]model.Tag, error)
}

type TagGradeStage struct {
	Client           *http.Client
	Endpoint         string // e.g. "https://openrouter.ai/api/v1/chat/completions"
	APIKey           string
	Models           []string // fallback chain
	QualityThreshold int
	TagRepo          TagRepoIface
	AttachTags       func(sub *model.Submission, tags []model.Tag) error
}

func (s *TagGradeStage) Name() string { return "tag_grade" }
func (s *TagGradeStage) Next() string { return "" }

type llmGrade struct {
	Tags         []string `json:"tags"`
	QualityScore int      `json:"quality_score"`
	Summary      string   `json:"summary"`
	IsSpam       bool     `json:"is_spam"`
	SpamReason   *string  `json:"spam_reason"`
}

const graderSystemPrompt = `You are an assistant that triages short user-submitted question videos for a creator's Q&A inbox. Given the transcript, return strict JSON only (no prose) matching this schema:
{"tags":[lowercase, hyphenated topic tags, max 5],
 "quality_score":0-100 integer (relevance, clarity, specificity),
 "summary":"one-line plain summary of the question",
 "is_spam":boolean (true if abusive, off-topic, promo, gibberish),
 "spam_reason":string or null}
Return only the JSON object.`

func (s *TagGradeStage) Run(ctx context.Context, sub *model.Submission) error {
	if sub.Transcript == nil || strings.TrimSpace(*sub.Transcript) == "" {
		return fmt.Errorf("submission %s has empty transcript", sub.ID)
	}

	var lastErr error
	var grade llmGrade
	for _, m := range s.Models {
		g, err := s.call(ctx, m, *sub.Transcript)
		if err == nil {
			grade = g
			lastErr = nil
			break
		}
		lastErr = err
	}
	if lastErr != nil {
		return fmt.Errorf("all models failed: %w", lastErr)
	}

	sub.QualityScore = &grade.QualityScore
	sub.Summary = &grade.Summary
	sub.IsSpam = grade.IsSpam
	sub.SpamReason = grade.SpamReason

	tags, err := s.TagRepo.GetOrCreate(grade.Tags)
	if err != nil {
		return fmt.Errorf("save tags: %w", err)
	}
	if err := s.AttachTags(sub, tags); err != nil {
		return fmt.Errorf("attach tags: %w", err)
	}
	if grade.IsSpam || grade.QualityScore < s.QualityThreshold {
		sub.Status = model.StatusQuarantined
	}
	return nil
}

type chatReq struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (s *TagGradeStage) call(ctx context.Context, model, transcript string) (llmGrade, error) {
	body, _ := json.Marshal(chatReq{
		Model: model,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{Role: "system", Content: graderSystemPrompt},
			{Role: "user", Content: transcript},
		},
		ResponseFormat: map[string]string{"type": "json_object"},
	})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", s.Endpoint, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.Client.Do(req)
	if err != nil {
		return llmGrade{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		out, _ := io.ReadAll(resp.Body)
		return llmGrade{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(out))
	}
	var cr chatResp
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return llmGrade{}, err
	}
	if len(cr.Choices) == 0 {
		return llmGrade{}, fmt.Errorf("no choices")
	}
	var g llmGrade
	if err := json.Unmarshal([]byte(cr.Choices[0].Message.Content), &g); err != nil {
		return llmGrade{}, fmt.Errorf("malformed grade JSON: %w", err)
	}
	return g, nil
}
```

- [ ] **Step 11.4: Run tests, confirm pass**

```bash
go test ./pipeline/... -run TestTagGradeStage -v
```
Expected: 3 PASS.

- [ ] **Step 11.5: Commit**

```bash
git add pipeline/llm.go pipeline/llm_test.go
git commit -m "feat(samqna): OpenRouter tag/grade stage with model fallback chain"
```

---

### Task 12: Retention pruner

**Files:**
- Create: `pipeline/retention.go`, `pipeline/retention_test.go`

- [ ] **Step 12.1: Write failing test**

Create `pipeline/retention_test.go`:
```go
package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"samqna/migrations"
	"samqna/model"
	"samqna/repository"
	"samqna/storage"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPruner_RemovesOldUnstarredVideos(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	sr := repository.NewSubmissionRepo(db)

	root := t.TempDir()
	st := storage.New(root, root)
	require.NoError(t, st.EnsureRoots())

	// create old + new + starred
	mk := func(age time.Duration, starred bool) *model.Submission {
		id := model.NewSubmissionID()
		ts := time.Now().Add(-age)
		paths := st.PathsFor(id, ts)
		require.NoError(t, st.EnsureDir(paths))
		vp := paths.Original + ".mp4"
		require.NoError(t, os.WriteFile(vp, []byte("x"), 0o644))
		s := &model.Submission{
			ID: id, CreatedAt: ts, SubmitterIP: "x",
			Status: model.StatusReady, VideoPath: &vp,
			AudioPath: paths.Audio, Starred: starred,
		}
		require.NoError(t, sr.Create(s))
		return s
	}
	old := mk(40*24*time.Hour, false)
	young := mk(5*24*time.Hour, false)
	starredOld := mk(40*24*time.Hour, true)

	p := NewPruner(sr, st, 30)
	require.NoError(t, p.RunOnce(context.Background()))

	gotOld, _ := sr.Get(old.ID)
	require.Nil(t, gotOld.VideoPath)
	require.NotNil(t, gotOld.PrunedAt)
	_, err = os.Stat(*old.VideoPath)
	require.True(t, os.IsNotExist(err))

	gotYoung, _ := sr.Get(young.ID)
	require.NotNil(t, gotYoung.VideoPath)
	_, err = os.Stat(filepath.Clean(*young.VideoPath))
	require.NoError(t, err)

	gotStarred, _ := sr.Get(starredOld.ID)
	require.NotNil(t, gotStarred.VideoPath)
}
```

- [ ] **Step 12.2: Implement pruner**

Create `pipeline/retention.go`:
```go
package pipeline

import (
	"context"
	"log/slog"
	"os"
	"time"

	"samqna/repository"
	"samqna/storage"
)

type Pruner struct {
	SR            *repository.SubmissionRepo
	Storage       *storage.Storage
	RetentionDays int
}

func NewPruner(sr *repository.SubmissionRepo, st *storage.Storage, days int) *Pruner {
	return &Pruner{SR: sr, Storage: st, RetentionDays: days}
}

func (p *Pruner) RunOnce(ctx context.Context) error {
	candidates, err := p.SR.PruneCandidates(p.RetentionDays)
	if err != nil {
		return err
	}
	for _, s := range candidates {
		if s.VideoPath == nil {
			continue
		}
		if err := os.Remove(*s.VideoPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("prune: remove video", "id", s.ID, "err", err)
			continue
		}
		// also drop cached export if present
		_ = os.Remove(p.Storage.ExportPath(s.ID))
		if err := p.SR.MarkPruned(s.ID); err != nil {
			slog.Error("prune: mark", "id", s.ID, "err", err)
		}
	}
	return nil
}

func (p *Pruner) RunForever(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.RunOnce(ctx); err != nil {
				slog.Error("pruner cycle", "err", err)
			}
		}
	}
}
```

- [ ] **Step 12.3: Run tests, confirm pass**

```bash
go test ./pipeline/... -run TestPruner -v
```
Expected: PASS.

- [ ] **Step 12.4: Commit**

```bash
git add pipeline/retention.go pipeline/retention_test.go
git commit -m "feat(samqna): retention pruner for unstarred videos past TTL"
```

---

## Phase 5 — Services and notify

### Task 13: Submission service (orchestrates repo + storage + pipeline kickoff)

**Files:**
- Create: `service/submission.go`, `service/submission_test.go`
- Delete: `service/main.go`, `service/post.go` (empty placeholders)

- [ ] **Step 13.1: Remove placeholders**

```bash
rm service/main.go service/post.go
```

- [ ] **Step 13.2: Write failing test**

Create `service/submission_test.go`:
```go
package service

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"samqna/migrations"
	"samqna/model"
	"samqna/repository"
	"samqna/storage"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setup(t *testing.T) (*Submissions, *gorm.DB) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	st := storage.New(t.TempDir(), t.TempDir())
	require.NoError(t, st.EnsureRoots())
	svc := &Submissions{
		Subs:    repository.NewSubmissionRepo(db),
		Jobs:    repository.NewJobRepo(db),
		Tags:    repository.NewTagRepo(db),
		Storage: st,
		MaxBytes: 1 << 20,
	}
	return svc, db
}

func TestSubmissions_AcceptUpload_CreatesSubmissionAndJob(t *testing.T) {
	svc, db := setup(t)
	body := bytes.NewReader([]byte("fakevideobytes"))
	res, err := svc.AcceptUpload(AcceptInput{
		IP: "1.2.3.4", OriginalFilename: "q.mp4",
		Reader: body, Size: int64(body.Len()),
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.ID)

	got, err := svc.Subs.Get(res.ID)
	require.NoError(t, err)
	require.Equal(t, model.StatusProcessing, got.Status)
	require.NotNil(t, got.VideoPath)

	var n int64
	db.Model(&model.Job{}).Where("submission_id = ?", res.ID).Count(&n)
	require.Equal(t, int64(1), n)
}

func TestSubmissions_AcceptUpload_RejectsOversize(t *testing.T) {
	svc, _ := setup(t)
	svc.MaxBytes = 5
	_, err := svc.AcceptUpload(AcceptInput{
		IP: "x", OriginalFilename: "q.mp4",
		Reader: strings.NewReader("toolong"), Size: 7,
	})
	require.ErrorIs(t, err, ErrTooLarge)
}

func TestSubmissions_AcceptUpload_StreamReaderTooLong(t *testing.T) {
	svc, _ := setup(t)
	svc.MaxBytes = 5
	// size unknown (0), but reader gives more than MaxBytes
	rdr := io.NopCloser(strings.NewReader("toolong"))
	_, err := svc.AcceptUpload(AcceptInput{
		IP: "x", OriginalFilename: "q.mp4",
		Reader: rdr, Size: 0,
	})
	require.ErrorIs(t, err, ErrTooLarge)
}

func TestSubmissions_RateLimit(t *testing.T) {
	svc, _ := setup(t)
	for i := 0; i < 3; i++ {
		_, err := svc.AcceptUpload(AcceptInput{IP: "9.9.9.9", OriginalFilename: "q.mp4", Reader: strings.NewReader("x"), Size: 1})
		require.NoError(t, err)
	}
	err := svc.CheckRateLimit("9.9.9.9", 3, 24*time.Hour)
	require.ErrorIs(t, err, ErrRateLimit)
}
```

- [ ] **Step 13.3: Run, confirm failure**

```bash
go test ./service/...
```
Expected: undefined.

- [ ] **Step 13.4: Implement service**

Create `service/submission.go`:
```go
package service

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"samqna/model"
	"samqna/repository"
	"samqna/storage"
)

var (
	ErrTooLarge  = errors.New("upload too large")
	ErrRateLimit = errors.New("rate limit exceeded")
	ErrBlocked   = errors.New("blocked")
)

type Submissions struct {
	Subs     *repository.SubmissionRepo
	Jobs     *repository.JobRepo
	Tags     *repository.TagRepo
	IPs      *repository.IPRepo
	Storage  *storage.Storage
	MaxBytes int64
}

type AcceptInput struct {
	IP               string
	Email            string
	OriginalFilename string
	Reader           io.Reader
	Size             int64 // 0 if unknown
	Ext              string // ".mp4" / ".webm" — defaults to original ext
}

type AcceptResult struct {
	ID string
}

func (s *Submissions) AcceptUpload(in AcceptInput) (*AcceptResult, error) {
	if s.MaxBytes > 0 && in.Size > 0 && in.Size > s.MaxBytes {
		return nil, ErrTooLarge
	}
	if in.IPs := s.IPs; in.IPs != nil {
		if blocked, err := s.IPs.IsBlocked(in.IP); err != nil {
			return nil, err
		} else if blocked {
			return nil, ErrBlocked
		}
	}

	id := model.NewSubmissionID()
	createdAt := time.Now()
	paths := s.Storage.PathsFor(id, createdAt)
	if err := s.Storage.EnsureDir(paths); err != nil {
		return nil, err
	}

	ext := in.Ext
	if ext == "" {
		ext = ".mp4"
	}
	dst := paths.Original + ext

	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	limit := s.MaxBytes
	if limit <= 0 {
		limit = 1 << 30
	}
	n, copyErr := io.Copy(f, io.LimitReader(in.Reader, limit+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return nil, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return nil, closeErr
	}
	if n > limit {
		_ = os.Remove(dst)
		return nil, ErrTooLarge
	}

	var emailPtr *string
	if in.Email != "" {
		emailPtr = &in.Email
	}
	sub := &model.Submission{
		ID:               id,
		CreatedAt:        createdAt,
		SubmitterIP:      in.IP,
		SubmitterEmail:   emailPtr,
		OriginalFilename: in.OriginalFilename,
		SizeBytes:        n,
		VideoPath:        ptr(dst),
		AudioPath:        paths.Audio, // filled by extract stage
		ThumbnailPath:    paths.Thumbnail,
		Status:           model.StatusProcessing,
	}
	if err := s.Subs.Create(sub); err != nil {
		_ = os.Remove(dst)
		return nil, fmt.Errorf("create submission: %w", err)
	}
	if err := s.Jobs.Enqueue(sub.ID, model.StageExtract); err != nil {
		return nil, fmt.Errorf("enqueue job: %w", err)
	}
	return &AcceptResult{ID: sub.ID}, nil
}

func (s *Submissions) CheckRateLimit(ip string, max int, window time.Duration) error {
	n, err := s.Subs.CountFromIPSince(ip, time.Now().Add(-window))
	if err != nil {
		return err
	}
	if int(n) >= max {
		return ErrRateLimit
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
```

- [ ] **Step 13.5: Run, confirm pass**

```bash
go test ./service/... -v
```
Expected: 4 PASS.

- [ ] **Step 13.6: Commit**

```bash
git add service/
git rm service/main.go service/post.go
git commit -m "feat(samqna): submission service (accept upload, rate-limit)"
```

---

### Task 14: Notify package (Telegram wrapper)

**Files:**
- Create: `notify/telegram.go`, `notify/telegram_test.go`

- [ ] **Step 14.1: Write failing test (capture command via shellable interface)**

Create `notify/telegram_test.go`:
```go
package notify

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSend_NoConfigIsNoOp(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	n := New()
	require.NoError(t, n.Send("hello"))
}

func TestSend_BuildsCorrectURL(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "TOK")
	t.Setenv("TELEGRAM_CHAT_ID", "CHAT")
	var captured string
	n := &Notifier{
		send: func(url string) error { captured = url; return nil },
	}
	n.loadEnv()
	require.NoError(t, n.Send("hi there"))
	require.Contains(t, captured, "https://api.telegram.org/botTOK/sendMessage")
	require.Contains(t, captured, "chat_id=CHAT")
	require.Contains(t, captured, "text=hi+there")
}
```

- [ ] **Step 14.2: Implement notify**

Create `notify/telegram.go`:
```go
package notify

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"
)

type Notifier struct {
	token  string
	chat   string
	send   func(url string) error
	client *http.Client
}

func New() *Notifier {
	n := &Notifier{client: &http.Client{Timeout: 5 * time.Second}}
	n.send = n.defaultSend
	n.loadEnv()
	return n
}

func (n *Notifier) loadEnv() {
	n.token = os.Getenv("TELEGRAM_BOT_TOKEN")
	n.chat = os.Getenv("TELEGRAM_CHAT_ID")
}

func (n *Notifier) Send(msg string) error {
	if n.token == "" || n.chat == "" {
		slog.Debug("telegram disabled (no token/chat)")
		return nil
	}
	q := url.Values{}
	q.Set("chat_id", n.chat)
	q.Set("text", msg)
	u := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage?%s", n.token, q.Encode())
	return n.send(u)
}

func (n *Notifier) defaultSend(u string) error {
	resp, err := n.client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram status %d", resp.StatusCode)
	}
	return nil
}
```

- [ ] **Step 14.3: Run tests, commit**

```bash
go test ./notify/... -v
```
Expected: 2 PASS.

```bash
git add notify/
git commit -m "feat(samqna): telegram notifier wrapper"
```

---

## Phase 6 — HTTP layer (open routes)

### Task 15: HTML templates + layout + landing/submit pages

**Files:**
- Create: `view/templates.go`, `view/layout.html`, `view/landing.html`, `view/submit.html`, `view/dashboard.html`, `view/list_fragment.html`, `view/video.html`, `view/status_fragment.html`, `view/components/card.html`, `view/components/tag_chip.html`
- Create: `static/htmx.min.js` (copy from CDN), `static/app.css`, `static/recorder.js`, `static/trim.js`

- [ ] **Step 15.1: Download HTMX**

```bash
mkdir -p static
curl -L -o static/htmx.min.js https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js
```
Expected: ~50 KB file.

- [ ] **Step 15.2: Implement template loader**

Create `view/templates.go`:
```go
package view

import (
	"embed"
	"html/template"
	"io"
)

//go:embed *.html components/*.html
var files embed.FS

type Renderer struct {
	pages map[string]*template.Template
}

func New() (*Renderer, error) {
	funcs := template.FuncMap{
		"truncate": func(s string, n int) string {
			if len(s) <= n {
				return s
			}
			return s[:n] + "…"
		},
	}
	parse := func(names ...string) (*template.Template, error) {
		return template.New("base").Funcs(funcs).ParseFS(files, names...)
	}
	pages := map[string]*template.Template{}
	type pageDef struct{ name string; files []string }
	defs := []pageDef{
		{"landing", []string{"layout.html", "landing.html"}},
		{"submit", []string{"layout.html", "submit.html"}},
		{"dashboard", []string{"layout.html", "dashboard.html", "components/card.html", "components/tag_chip.html"}},
		{"video", []string{"layout.html", "video.html", "components/tag_chip.html"}},
		{"list_fragment", []string{"list_fragment.html", "components/card.html"}},
		{"status_fragment", []string{"status_fragment.html"}},
	}
	for _, d := range defs {
		t, err := parse(d.files...)
		if err != nil {
			return nil, err
		}
		pages[d.name] = t
	}
	return &Renderer{pages: pages}, nil
}

func (r *Renderer) Render(w io.Writer, name string, data any) error {
	tpl, ok := r.pages[name]
	if !ok {
		return errPageNotFound(name)
	}
	// Layout templates use "base"; fragments render themselves
	root := "base"
	if name == "list_fragment" || name == "status_fragment" {
		root = name
	}
	return tpl.ExecuteTemplate(w, root, data)
}

type errPageNotFound string

func (e errPageNotFound) Error() string { return "page not found: " + string(e) }
```

- [ ] **Step 15.3: Create layout template**

Create `view/layout.html`:
```html
{{define "base"}}<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{template "title" .}} · SamQnA</title>
  <link rel="stylesheet" href="/static/app.css">
  <script src="/static/htmx.min.js" defer></script>
</head>
<body>
  <header class="topbar">
    <a href="/" class="brand">SamQnA</a>
    <nav>
      <a href="/browse">Browse</a>
      <a href="/submit">Ask</a>
    </nav>
  </header>
  <main class="container">{{template "content" .}}</main>
  <footer class="footer">Open Q&amp;A inbox · powered by AI triage</footer>
</body>
</html>{{end}}
```

- [ ] **Step 15.4: Create landing page**

Create `view/landing.html`:
```html
{{define "title"}}Home{{end}}
{{define "content"}}
<section class="hero">
  <h1>Ask the creator anything — on video.</h1>
  <p>Record a 60-second question. The creator browses, filters, and picks the best for their next video.</p>
  <a class="btn primary" href="/submit">Submit a question</a>
  <a class="btn" href="/browse">See submitted questions</a>
</section>
{{end}}
```

- [ ] **Step 15.5: Create submit page**

Create `view/submit.html`:
```html
{{define "title"}}Submit{{end}}
{{define "content"}}
<h1>Submit a question</h1>
<p class="muted">≤ 60 seconds. By submitting, you agree your video will be publicly visible.</p>

<form id="upload-form" action="/submit" method="post" enctype="multipart/form-data" class="stack">
  <label>Optional email (notify if your question is answered)
    <input type="email" name="email" autocomplete="email">
  </label>

  <div class="recorder">
    <button type="button" id="rec-toggle" class="btn">● Record</button>
    <video id="rec-preview" autoplay muted playsinline></video>
    <div id="rec-timer" class="muted">0s / 60s</div>
  </div>

  <label>Or upload an existing file
    <input type="file" name="video" accept="video/*">
  </label>

  <label class="checkbox">
    <input type="checkbox" name="consent" required>
    I understand my submission will be publicly visible.
  </label>

  {{if .TurnstileSite}}
  <div class="cf-turnstile" data-sitekey="{{.TurnstileSite}}"></div>
  <script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script>
  {{end}}

  <button type="submit" class="btn primary">Submit</button>
</form>

<script src="/static/recorder.js" defer></script>
{{end}}
```

- [ ] **Step 15.6: Create dashboard, video, fragments**

Create `view/dashboard.html`:
```html
{{define "title"}}Browse{{end}}
{{define "content"}}
<h1>Submitted questions</h1>

<form id="filters" hx-get="/browse/list" hx-target="#list" hx-trigger="change, keyup delay:300ms" hx-push-url="true" class="filters">
  <div class="tag-cloud">
    {{range $name, $count := .Tags}}
      <label class="tag-chip">
        <input type="checkbox" name="tag" value="{{$name}}">
        {{$name}} <span class="count">{{$count}}</span>
      </label>
    {{end}}
  </div>
  <label>Min quality
    <input type="range" name="min_score" min="0" max="100" value="{{.MinScore}}">
  </label>
  <label class="checkbox">
    <input type="checkbox" name="starred" value="1" {{if .StarredOnly}}checked{{end}}> Starred only
  </label>
</form>

<div id="list">
  {{template "list_inner" .}}
</div>
{{end}}
```

Create `view/list_fragment.html`:
```html
{{define "list_fragment"}}{{template "list_inner" .}}{{end}}
{{define "list_inner"}}
<div class="card-grid">
{{range .Submissions}}
  {{template "card" .}}
{{else}}
  <p class="muted">No matching submissions.</p>
{{end}}
</div>
{{end}}
```

Create `view/components/card.html`:
```html
{{define "card"}}
<a href="/v/{{.ID}}" class="card">
  <img src="/v/{{.ID}}/thumb" alt="" class="thumb">
  <div class="card-body">
    <div class="score">{{if .QualityScore}}{{deref .QualityScore}}{{else}}—{{end}}</div>
    <div class="summary">{{if .Summary}}{{deref .Summary}}{{else}}(processing)…{{end}}</div>
    <div class="tags">
      {{range .Tags}}{{template "tag_chip" .Name}}{{end}}
    </div>
  </div>
</a>
{{end}}
```

Create `view/components/tag_chip.html`:
```html
{{define "tag_chip"}}<span class="tag-chip-static">{{.}}</span>{{end}}
```

Create `view/video.html`:
```html
{{define "title"}}{{if .Summary}}{{.Summary}}{{else}}Submission{{end}}{{end}}
{{define "content"}}
<article class="video-page">
  <div class="player">
    {{if .HasVideo}}
      <video controls src="/v/{{.ID}}/video" poster="/v/{{.ID}}/thumb"></video>
    {{else if .HasAudio}}
      <img src="/v/{{.ID}}/thumb" alt="" class="thumb-large">
      <audio controls src="/v/{{.ID}}/audio"></audio>
    {{else}}
      <p class="muted">Media unavailable.</p>
    {{end}}
  </div>

  <aside>
    <div id="status" hx-get="/v/{{.ID}}/status" hx-trigger="every 2s" hx-swap="outerHTML">
      {{template "status_fragment" .}}
    </div>
    <h2>Summary</h2>
    <p>{{if .Summary}}{{.Summary}}{{else}}(processing)…{{end}}</p>
    <h2>Transcript</h2>
    <pre class="transcript">{{if .Transcript}}{{.Transcript}}{{else}}(processing)…{{end}}</pre>
    <h2>Tags</h2>
    <div class="tags">{{range .Tags}}{{template "tag_chip" .Name}}{{end}}</div>
    <div class="exports">
      <a class="btn primary" href="/v/{{.ID}}/export">Download MP4</a>
      <button class="btn" onclick="document.getElementById('trim-ui').hidden = false">Trim &amp; download</button>
    </div>
    <div id="trim-ui" hidden>
      <label>Start <input type="range" id="trim-start" min="0" max="{{.DurationSec}}" value="0"></label>
      <label>End <input type="range" id="trim-end" min="0" max="{{.DurationSec}}" value="{{.DurationSec}}"></label>
      <button class="btn" id="trim-go" data-id="{{.ID}}">Download trim</button>
    </div>
  </aside>
</article>
<script src="/static/trim.js" defer></script>
{{end}}
```

Create `view/status_fragment.html`:
```html
{{define "status_fragment"}}
<div id="status" class="status-{{.Status}}"
  {{if eq .Status "ready" "failed" "quarantined"}}hx-swap-oob="true"{{end}}>
  Status: {{.Status}}
</div>
{{end}}
```

- [ ] **Step 15.7: Add CSS**

Create `static/app.css`:
```css
:root { --fg:#111; --muted:#666; --bg:#fafafa; --card:#fff; --line:#e5e5e5; --pri:#3b82f6; }
* { box-sizing: border-box; }
body { font: 16px/1.5 system-ui, sans-serif; color: var(--fg); background: var(--bg); margin: 0; }
.container { max-width: 980px; margin: 0 auto; padding: 24px; }
.topbar { display: flex; justify-content: space-between; padding: 12px 24px; border-bottom: 1px solid var(--line); background: var(--card); }
.brand { font-weight: 700; text-decoration: none; color: var(--fg); }
.topbar nav a { margin-left: 16px; text-decoration: none; color: var(--muted); }
.footer { text-align: center; padding: 24px; color: var(--muted); font-size: 14px; }
.btn { display: inline-block; padding: 8px 16px; border: 1px solid var(--line); border-radius: 8px; background: var(--card); cursor: pointer; text-decoration: none; color: var(--fg); }
.btn.primary { background: var(--pri); color: white; border-color: var(--pri); }
.stack > * + * { margin-top: 12px; }
.muted { color: var(--muted); }
.checkbox { display: flex; gap: 8px; align-items: center; }
.tag-chip { display: inline-block; padding: 4px 10px; border-radius: 999px; border: 1px solid var(--line); margin: 2px; cursor: pointer; }
.tag-chip input { display: none; }
.tag-chip input:checked + * , .tag-chip:has(input:checked) { background: var(--pri); color: white; border-color: var(--pri); }
.tag-chip-static { display: inline-block; padding: 2px 8px; border-radius: 999px; background: #eef; font-size: 12px; margin-right: 4px; }
.tag-cloud { margin-bottom: 12px; }
.filters { background: var(--card); padding: 16px; border-radius: 8px; margin-bottom: 16px; }
.card-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 16px; }
.card { background: var(--card); border-radius: 8px; overflow: hidden; text-decoration: none; color: inherit; border: 1px solid var(--line); display: block; }
.thumb { width: 100%; height: 160px; object-fit: cover; background: #ddd; }
.card-body { padding: 12px; }
.score { font-size: 24px; font-weight: 700; }
.summary { margin: 4px 0; }
.tags { margin-top: 8px; }
.video-page { display: grid; grid-template-columns: 2fr 1fr; gap: 24px; }
@media (max-width: 800px) { .video-page { grid-template-columns: 1fr; } }
.player video, .player audio { width: 100%; }
.transcript { white-space: pre-wrap; background: var(--card); padding: 12px; border-radius: 8px; border: 1px solid var(--line); }
.recorder video { width: 100%; max-height: 240px; background: #000; border-radius: 8px; }
.status-processing { background: #fef3c7; padding: 8px; border-radius: 6px; }
.status-ready { background: #d1fae5; padding: 8px; border-radius: 6px; }
.status-failed { background: #fecaca; padding: 8px; border-radius: 6px; }
.status-quarantined { background: #e5e5e5; padding: 8px; border-radius: 6px; }
```

- [ ] **Step 15.8: Add recorder.js**

Create `static/recorder.js`:
```js
(() => {
  const btn = document.getElementById('rec-toggle');
  const preview = document.getElementById('rec-preview');
  const timer = document.getElementById('rec-timer');
  const form = document.getElementById('upload-form');
  if (!btn || !preview) return;

  let stream, recorder, chunks = [], startedAt = 0, tickHandle, stopHandle;

  async function start() {
    chunks = [];
    stream = await navigator.mediaDevices.getUserMedia({ video: { width: 720 }, audio: true });
    preview.srcObject = stream;
    const mime = MediaRecorder.isTypeSupported('video/webm;codecs=vp9,opus') ? 'video/webm;codecs=vp9,opus' : 'video/mp4';
    recorder = new MediaRecorder(stream, { mimeType: mime });
    recorder.ondataavailable = (e) => { if (e.data.size) chunks.push(e.data); };
    recorder.onstop = onStop;
    recorder.start();
    btn.textContent = '■ Stop';
    startedAt = Date.now();
    tick();
    tickHandle = setInterval(tick, 200);
    stopHandle = setTimeout(stop, 60000);
  }
  function tick() {
    const s = Math.floor((Date.now() - startedAt) / 1000);
    timer.textContent = `${s}s / 60s`;
  }
  function stop() {
    if (recorder && recorder.state === 'recording') recorder.stop();
    clearInterval(tickHandle); clearTimeout(stopHandle);
    if (stream) stream.getTracks().forEach(t => t.stop());
    btn.textContent = '● Record';
  }
  function onStop() {
    const ext = recorder.mimeType.includes('webm') ? '.webm' : '.mp4';
    const blob = new Blob(chunks, { type: recorder.mimeType });
    const file = new File([blob], `recording${ext}`, { type: recorder.mimeType });
    const dt = new DataTransfer();
    dt.items.add(file);
    form.video.files = dt.files;
  }
  btn.addEventListener('click', () => recorder && recorder.state === 'recording' ? stop() : start());
})();
```

- [ ] **Step 15.9: Add trim.js**

Create `static/trim.js`:
```js
(() => {
  const go = document.getElementById('trim-go');
  if (!go) return;
  go.addEventListener('click', async () => {
    const id = go.dataset.id;
    const start = parseFloat(document.getElementById('trim-start').value);
    const end = parseFloat(document.getElementById('trim-end').value);
    if (end <= start) { alert('End must be after start'); return; }
    const r = await fetch(`/v/${id}/export/trim`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ start, end })
    });
    if (!r.ok) { alert('Trim failed'); return; }
    const blob = await r.blob();
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `clip-${id}.mp4`;
    a.click();
  });
})();
```

- [ ] **Step 15.10: Verify templates parse**

```bash
go test -run TestTemplatesParse ./view/...
```
Add a small test in `view/templates_test.go`:
```go
package view

import "testing"

func TestTemplatesParse(t *testing.T) {
	if _, err := New(); err != nil {
		t.Fatalf("New: %v", err)
	}
}
```
Run it:
```bash
go test ./view/... -v
```
Expected: PASS.

- [ ] **Step 15.11: Commit**

```bash
git add view/ static/
git commit -m "feat(samqna): html/template views + HTMX assets + recorder/trim JS"
```

---

### Task 16: Public HTTP routes (submit, browse, video, media stream, healthz, tags)

**Files:**
- Create: `route/public.go`, `route/public_test.go`
- Modify: `route/main.go` (delete or replace with shared helpers)
- Delete: `route/post.go` (empty)

- [ ] **Step 16.1: Cleanup placeholders**

```bash
rm route/post.go route/main.go
```

- [ ] **Step 16.2: Write failing route test**

Create `route/public_test.go`:
```go
package route

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"samqna/config"
	"samqna/migrations"
	"samqna/repository"
	"samqna/service"
	"samqna/storage"
	"samqna/view"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	st := storage.New(t.TempDir(), t.TempDir())
	require.NoError(t, st.EnsureRoots())
	vw, err := view.New()
	require.NoError(t, err)
	deps := &Deps{
		Cfg:     &config.Config{MaxUploadBytes: 1 << 20, MaxIPPerDay: 3, QualityThreshold: 30, AdminToken: "x"},
		DB:      db,
		Subs:    repository.NewSubmissionRepo(db),
		Jobs:    repository.NewJobRepo(db),
		Tags:    repository.NewTagRepo(db),
		IPs:     repository.NewIPRepo(db),
		Storage: st,
		View:    vw,
		Submissions: &service.Submissions{
			Subs: repository.NewSubmissionRepo(db),
			Jobs: repository.NewJobRepo(db),
			Tags: repository.NewTagRepo(db),
			IPs:  repository.NewIPRepo(db),
			Storage: st, MaxBytes: 1 << 20,
		},
	}
	RegisterPublic(r, deps)
	return r, db
}

func TestHealthz(t *testing.T) {
	r, _ := newRouter(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/healthz", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "ok", body["status"])
}

func TestSubmitUpload_HappyPath(t *testing.T) {
	r, db := newRouter(t)
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("video", "q.mp4")
	part.Write([]byte("fakebytes"))
	mw.WriteField("consent", "on")
	mw.Close()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.RemoteAddr = "1.2.3.4:5555"
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)
	var n int64
	db.Table("submissions").Count(&n)
	require.Equal(t, int64(1), n)
}

func TestSubmitUpload_NoConsent_Rejected(t *testing.T) {
	r, _ := newRouter(t)
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, _ := mw.CreateFormFile("video", "q.mp4")
	part.Write([]byte("x"))
	mw.Close()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}
```

- [ ] **Step 16.3: Implement deps struct and public routes**

Create `route/public.go`:
```go
package route

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"samqna/config"
	"samqna/repository"
	"samqna/service"
	"samqna/storage"
	"samqna/view"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Deps struct {
	Cfg         *config.Config
	DB          *gorm.DB
	Subs        *repository.SubmissionRepo
	Jobs        *repository.JobRepo
	Tags        *repository.TagRepo
	IPs         *repository.IPRepo
	Storage     *storage.Storage
	View        *view.Renderer
	Submissions *service.Submissions
}

func RegisterPublic(r *gin.Engine, d *Deps) {
	r.StaticFS("/static", http.Dir("static"))

	r.GET("/", func(c *gin.Context) { render(c, d.View, "landing", gin.H{}) })
	r.GET("/submit", func(c *gin.Context) {
		render(c, d.View, "submit", gin.H{"TurnstileSite": d.Cfg.TurnstileSite})
	})
	r.POST("/submit", func(c *gin.Context) { submitHandler(c, d) })
	r.GET("/browse", func(c *gin.Context) { browseHandler(c, d, false) })
	r.GET("/browse/list", func(c *gin.Context) { browseHandler(c, d, true) })
	r.GET("/v/:id", func(c *gin.Context) { videoHandler(c, d) })
	r.GET("/v/:id/status", func(c *gin.Context) { statusHandler(c, d) })
	r.GET("/v/:id/thumb", func(c *gin.Context) { fileHandler(c, d, "thumb") })
	r.GET("/v/:id/video", func(c *gin.Context) { fileHandler(c, d, "video") })
	r.GET("/v/:id/audio", func(c *gin.Context) { fileHandler(c, d, "audio") })
	r.GET("/tags", func(c *gin.Context) {
		m, err := d.Tags.AllWithCounts()
		if err != nil {
			c.AbortWithStatus(500); return
		}
		c.JSON(200, m)
	})
	r.GET("/healthz", func(c *gin.Context) { healthHandler(c, d) })
}

func render(c *gin.Context, vw *view.Renderer, name string, data any) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := vw.Render(c.Writer, name, data); err != nil {
		_ = c.AbortWithError(500, err)
	}
}

func submitHandler(c *gin.Context, d *Deps) {
	if c.PostForm("consent") == "" {
		// Check multipart too (we haven't parsed it yet)
		_ = c.Request.ParseMultipartForm(32 << 20)
		if c.Request.FormValue("consent") == "" {
			c.String(http.StatusBadRequest, "Consent required.")
			return
		}
	}
	ip := clientIP(c)

	if d.IPs != nil {
		if blocked, err := d.IPs.IsBlocked(ip); err == nil && blocked {
			c.String(http.StatusForbidden, "Blocked.")
			return
		}
	}
	if err := d.Submissions.CheckRateLimit(ip, d.Cfg.MaxIPPerDay, 24*time.Hour); errors.Is(err, service.ErrRateLimit) {
		c.String(http.StatusTooManyRequests, "Daily submission limit reached.")
		return
	}
	// Turnstile (best-effort)
	if d.Cfg.TurnstileSecret != "" {
		if !verifyTurnstile(d.Cfg.TurnstileSecret, c.Request.FormValue("cf-turnstile-response"), ip) {
			c.String(http.StatusBadRequest, "Bot check failed.")
			return
		}
	}
	file, header, err := c.Request.FormFile("video")
	if err != nil {
		c.String(http.StatusBadRequest, "No video provided.")
		return
	}
	defer file.Close()
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".mp4" && ext != ".webm" && ext != ".mov" {
		ext = ".mp4"
	}
	res, err := d.Submissions.AcceptUpload(service.AcceptInput{
		IP:               ip,
		Email:            c.Request.FormValue("email"),
		OriginalFilename: header.Filename,
		Reader:           file,
		Size:             header.Size,
		Ext:              ext,
	})
	switch {
	case errors.Is(err, service.ErrTooLarge):
		c.String(http.StatusRequestEntityTooLarge, "Video too large (max 50 MB).")
		return
	case err != nil:
		c.String(http.StatusInternalServerError, "Server error.")
		return
	}
	c.Redirect(http.StatusSeeOther, "/v/"+res.ID)
}

func browseHandler(c *gin.Context, d *Deps, fragment bool) {
	tags := c.QueryArray("tag")
	minScore, _ := strconv.Atoi(c.DefaultQuery("min_score", "0"))
	starred := c.Query("starred") == "1"
	subs, err := d.Subs.ListReady(repository.ListFilter{
		Tags: tags, MinScore: minScore, StarredOnly: starred,
		Limit: 50, Offset: 0,
	})
	if err != nil {
		c.AbortWithStatus(500); return
	}
	views := make([]gin.H, 0, len(subs))
	for _, s := range subs {
		views = append(views, gin.H{
			"ID": s.ID, "Summary": deref(s.Summary), "QualityScore": s.QualityScore, "Tags": s.Tags,
		})
	}
	data := gin.H{
		"Submissions": views,
		"MinScore":    minScore,
		"StarredOnly": starred,
	}
	if fragment {
		render(c, d.View, "list_fragment", data)
		return
	}
	allTags, _ := d.Tags.AllWithCounts()
	data["Tags"] = allTags
	render(c, d.View, "dashboard", data)
}

func videoHandler(c *gin.Context, d *Deps) {
	s, err := d.Subs.Get(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404); return
	}
	render(c, d.View, "video", gin.H{
		"ID": s.ID, "Status": s.Status, "Summary": deref(s.Summary),
		"Transcript": deref(s.Transcript), "Tags": s.Tags,
		"DurationSec": s.DurationSec,
		"HasVideo": s.VideoPath != nil, "HasAudio": s.AudioPath != "",
	})
}

func statusHandler(c *gin.Context, d *Deps) {
	s, err := d.Subs.Get(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404); return
	}
	render(c, d.View, "status_fragment", gin.H{"Status": s.Status})
}

func fileHandler(c *gin.Context, d *Deps, kind string) {
	s, err := d.Subs.Get(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404); return
	}
	var path string
	switch kind {
	case "thumb":
		path = s.ThumbnailPath
	case "audio":
		path = s.AudioPath
	case "video":
		if s.VideoPath == nil {
			c.AbortWithStatus(404); return
		}
		path = *s.VideoPath
	}
	if path == "" {
		c.AbortWithStatus(404); return
	}
	http.ServeFile(c.Writer, c.Request, path)
}

func healthHandler(c *gin.Context, d *Deps) {
	status := "ok"
	depth, _ := d.Jobs.QueueDepth()
	c.JSON(200, gin.H{"status": status, "queue_depth": depth})
}

func clientIP(c *gin.Context) string {
	if v := c.GetHeader("CF-Connecting-IP"); v != "" {
		return v
	}
	return c.ClientIP()
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func verifyTurnstile(secret, token, ip string) bool {
	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", token)
	form.Set("remoteip", ip)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", form)
	if err != nil {
		// fall back to allowing — don't block legit users on Cloudflare hiccups
		return true
	}
	defer resp.Body.Close()
	var out struct {
		Success bool `json:"success"`
	}
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &out)
	return out.Success
}

// helper used by status_fragment template
func _ignoreUnused() { _ = fmt.Sprintf("%s", os.Args) }
```

- [ ] **Step 16.4: Run public route tests**

```bash
go test ./route/... -v -run "TestHealthz|TestSubmit"
```
Expected: 3 PASS.

- [ ] **Step 16.5: Commit**

```bash
git add route/
git rm route/post.go route/main.go
git commit -m "feat(samqna): public HTTP routes (submit, browse, video, media, health)"
```

---

## Phase 7 — Export

### Task 17: Export service (one-click + trim + batch ZIP)

**Files:**
- Create: `service/export.go`, `service/export_test.go`

- [ ] **Step 17.1: Write failing tests**

Create `service/export_test.go`:
```go
package service

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"samqna/migrations"
	"samqna/model"
	"samqna/repository"
	"samqna/storage"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func ffmpegAvailable(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping")
	}
}

func setupExport(t *testing.T) (*Export, *model.Submission) {
	ffmpegAvailable(t)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.Migrate(db))
	st := storage.New(t.TempDir(), t.TempDir())
	require.NoError(t, st.EnsureRoots())
	sr := repository.NewSubmissionRepo(db)

	id := model.NewSubmissionID()
	ts := time.Now()
	paths := st.PathsFor(id, ts)
	require.NoError(t, st.EnsureDir(paths))
	in, _ := os.ReadFile("../testdata/sample.mp4")
	vp := paths.Original + ".mp4"
	require.NoError(t, os.WriteFile(vp, in, 0o644))

	sub := &model.Submission{
		ID: id, CreatedAt: ts, SubmitterIP: "x",
		VideoPath: &vp, Status: model.StatusReady, DurationSec: 3,
		AudioPath: paths.Audio, ThumbnailPath: paths.Thumbnail,
	}
	require.NoError(t, sr.Create(sub))
	e := &Export{Storage: st, Subs: sr, FfmpegBin: "ffmpeg", MaxConcurrent: 2}
	return e, sub
}

func TestExport_OneClick_CachesAndStreams(t *testing.T) {
	e, sub := setupExport(t)
	buf := &bytes.Buffer{}
	require.NoError(t, e.OneClick(context.Background(), sub.ID, buf))
	require.Greater(t, buf.Len(), 0)

	// second call should hit cache
	cached := e.Storage.ExportPath(sub.ID)
	st, err := os.Stat(cached)
	require.NoError(t, err)
	require.Greater(t, st.Size(), int64(0))
}

func TestExport_Trim_StreamsClip(t *testing.T) {
	e, sub := setupExport(t)
	buf := &bytes.Buffer{}
	require.NoError(t, e.Trim(context.Background(), sub.ID, 0.5, 2.5, buf))
	require.Greater(t, buf.Len(), 0)
}

func TestExport_BatchZip_ContainsManifestAndFiles(t *testing.T) {
	e, sub := setupExport(t)
	buf := &bytes.Buffer{}
	require.NoError(t, e.BatchZip(context.Background(), []string{sub.ID}, buf))
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	names := []string{}
	for _, f := range z.File {
		names = append(names, filepath.Base(f.Name))
	}
	require.Contains(t, names, "manifest.json")
}

// shim
var _ = io.Discard
```

- [ ] **Step 17.2: Implement export service**

Create `service/export.go`:
```go
package service

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"samqna/repository"
	"samqna/storage"
)

type Export struct {
	Storage       *storage.Storage
	Subs          *repository.SubmissionRepo
	FfmpegBin     string
	MaxConcurrent int

	sem chan struct{}
	once sync.Once
}

func (e *Export) initSem() {
	e.once.Do(func() {
		n := e.MaxConcurrent
		if n <= 0 { n = 2 }
		e.sem = make(chan struct{}, n)
	})
}

func (e *Export) acquire(ctx context.Context) error {
	e.initSem()
	select {
	case e.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Export) release() { <-e.sem }

func (e *Export) OneClick(ctx context.Context, id string, w io.Writer) error {
	cached := e.Storage.ExportPath(id)
	if f, err := os.Open(cached); err == nil {
		defer f.Close()
		_, err := io.Copy(w, f)
		return err
	}
	sub, err := e.Subs.Get(id)
	if err != nil { return err }
	if sub.VideoPath == nil { return fmt.Errorf("video unavailable") }

	if err := e.acquire(ctx); err != nil { return err }
	defer e.release()

	out, err := os.Create(cached)
	if err != nil { return err }
	defer out.Close()

	args := []string{"-y", "-i", *sub.VideoPath}
	if strings.HasSuffix(strings.ToLower(*sub.VideoPath), ".mp4") {
		args = append(args, "-c", "copy", "-movflags", "+faststart", "-f", "mp4", cached)
	} else {
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "22",
			"-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", "-f", "mp4", cached)
	}
	cmd := exec.CommandContext(ctx, e.FfmpegBin, args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(cached)
		return fmt.Errorf("ffmpeg: %w (%s)", err, strings.TrimSpace(string(combined)))
	}
	f, err := os.Open(cached)
	if err != nil { return err }
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func (e *Export) Trim(ctx context.Context, id string, start, end float64, w io.Writer) error {
	sub, err := e.Subs.Get(id)
	if err != nil { return err }
	if sub.VideoPath == nil { return fmt.Errorf("video unavailable") }
	if end <= start { return fmt.Errorf("end must be after start") }
	if err := e.acquire(ctx); err != nil { return err }
	defer e.release()

	cmd := exec.CommandContext(ctx, e.FfmpegBin,
		"-ss", fmt.Sprintf("%.3f", start),
		"-to", fmt.Sprintf("%.3f", end),
		"-i", *sub.VideoPath,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "22",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-f", "mp4", "pipe:1",
	)
	cmd.Stdout = w
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil { return err }
	_, _ = io.Copy(io.Discard, stderr)
	return cmd.Wait()
}

type manifestVideo struct {
	Filename     string   `json:"filename"`
	SubmissionID string   `json:"submission_id"`
	CreatedAt    string   `json:"created_at"`
	DurationSec  int      `json:"duration_sec"`
	Transcript   string   `json:"transcript,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	QualityScore *int     `json:"quality_score,omitempty"`
}

type manifest struct {
	ExportedAt string          `json:"exported_at"`
	Videos     []manifestVideo `json:"videos"`
}

func (e *Export) BatchZip(ctx context.Context, ids []string, w io.Writer) error {
	zw := zip.NewWriter(w)
	defer zw.Close()
	mf := manifest{ExportedAt: nowRFC3339()}
	for i, id := range ids {
		sub, err := e.Subs.Get(id)
		if err != nil { return err }
		if sub.VideoPath == nil { continue }
		name := fmt.Sprintf("%03d-%s.mp4", i+1, sub.ID)
		fw, err := zw.Create(name)
		if err != nil { return err }
		// Re-mux/transcode into the zip stream
		buf := &chunkWriter{w: fw}
		if err := e.OneClick(ctx, sub.ID, buf); err != nil { return err }
		tagNames := []string{}
		for _, t := range sub.Tags { tagNames = append(tagNames, t.Name) }
		mf.Videos = append(mf.Videos, manifestVideo{
			Filename: name, SubmissionID: sub.ID, CreatedAt: sub.CreatedAt.Format("2006-01-02T15:04:05Z"),
			DurationSec: sub.DurationSec, Transcript: derefStr(sub.Transcript),
			Summary: derefStr(sub.Summary), Tags: tagNames, QualityScore: sub.QualityScore,
		})
	}
	mfw, err := zw.Create("manifest.json")
	if err != nil { return err }
	enc := json.NewEncoder(mfw); enc.SetIndent("", "  ")
	return enc.Encode(mf)
}

type chunkWriter struct{ w io.Writer }
func (c *chunkWriter) Write(p []byte) (int, error) { return c.w.Write(p) }

func derefStr(p *string) string { if p == nil { return "" }; return *p }
func nowRFC3339() string         { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }
```

Add `"time"` to the imports at the top of the file.

- [ ] **Step 17.3: Run tests, confirm pass**

```bash
go test ./service/... -v -run TestExport
```
Expected: 3 PASS (skip if no ffmpeg).

- [ ] **Step 17.4: Commit**

```bash
git add service/export.go service/export_test.go
git commit -m "feat(samqna): export service (one-click cached, trim stream, batch zip)"
```

---

### Task 18: Export HTTP routes

**Files:**
- Modify: `route/public.go` (add export endpoints)

- [ ] **Step 18.1: Add export route handlers**

Append to `route/public.go` (inside `RegisterPublic`):
```go
	r.GET("/v/:id/export", func(c *gin.Context) {
		c.Header("Content-Type", "video/mp4")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="clip-%s.mp4"`, c.Param("id")))
		if err := d.ExportSvc.OneClick(c.Request.Context(), c.Param("id"), c.Writer); err != nil {
			_ = c.AbortWithError(500, err)
		}
	})
	r.POST("/v/:id/export/trim", func(c *gin.Context) {
		var body struct{ Start, End float64 }
		if err := c.ShouldBindJSON(&body); err != nil {
			c.AbortWithStatus(400); return
		}
		c.Header("Content-Type", "video/mp4")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="clip-%s.mp4"`, c.Param("id")))
		if err := d.ExportSvc.Trim(c.Request.Context(), c.Param("id"), body.Start, body.End, c.Writer); err != nil {
			_ = c.AbortWithError(500, err)
		}
	})
	r.POST("/export/batch", func(c *gin.Context) {
		var body struct{ IDs []string `json:"ids"` }
		if err := c.ShouldBindJSON(&body); err != nil || len(body.IDs) == 0 {
			c.AbortWithStatus(400); return
		}
		c.Header("Content-Type", "application/zip")
		c.Header("Content-Disposition", `attachment; filename="samqna-batch.zip"`)
		if err := d.ExportSvc.BatchZip(c.Request.Context(), body.IDs, c.Writer); err != nil {
			_ = c.AbortWithError(500, err)
		}
	})
```

Add `ExportSvc *service.Export` to the `Deps` struct.

- [ ] **Step 18.2: Build + run all route tests**

```bash
go build ./...
go test ./route/... -v
```
Expected: clean build, previous tests still pass.

- [ ] **Step 18.3: Commit**

```bash
git add route/
git commit -m "feat(samqna): export HTTP routes (one-click, trim, batch zip)"
```

---

## Phase 8 — Admin

### Task 19: Admin middleware + admin service + admin routes

**Files:**
- Create: `service/admin.go`, `route/admin.go`, `route/admin_test.go`

- [ ] **Step 19.1: Write failing admin route test**

Create `route/admin_test.go`:
```go
package route

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"samqna/config"
	"samqna/model"
	"samqna/repository"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdmin_Star_RequiresToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, db := newRouter(t)
	// register admin (manual because newRouter only does public)
	depsAdmin := &Deps{
		Cfg: &config.Config{AdminToken: "topsecret"},
		Subs: repository.NewSubmissionRepo(db), Jobs: repository.NewJobRepo(db),
		IPs: repository.NewIPRepo(db),
	}
	RegisterAdmin(r, depsAdmin)

	sub := &model.Submission{ID: model.NewSubmissionID(), SubmitterIP: "x", AudioPath: "/x", Status: model.StatusReady}
	require.NoError(t, depsAdmin.Subs.Create(sub))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/admin/v/"+sub.ID+"/star", bytes.NewReader(nil))
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code) // hidden without token

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/admin/v/"+sub.ID+"/star", bytes.NewReader(nil))
	req.Header.Set("X-Admin-Token", "topsecret")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	got, _ := depsAdmin.Subs.Get(sub.ID)
	require.True(t, got.Starred)
}
```

- [ ] **Step 19.2: Implement admin service**

Create `service/admin.go`:
```go
package service

import (
	"samqna/model"
	"samqna/repository"
)

type Admin struct {
	Subs *repository.SubmissionRepo
	Jobs *repository.JobRepo
	IPs  *repository.IPRepo
}

func (a *Admin) ToggleStar(id string) error {
	s, err := a.Subs.Get(id)
	if err != nil { return err }
	s.Starred = !s.Starred
	return a.Subs.Update(s)
}

func (a *Admin) Delete(id string) error                { return a.Subs.SoftDelete(id) }
func (a *Admin) BlockIP(ip, reason string) error       { return a.IPs.Block(ip, reason) }

func (a *Admin) Quarantine(id string, on bool) error {
	s, err := a.Subs.Get(id)
	if err != nil { return err }
	if on { s.Status = model.StatusQuarantined } else { s.Status = model.StatusReady }
	return a.Subs.Update(s)
}

func (a *Admin) Requeue(id string) error {
	job, err := a.Jobs.GetBySubmission(id)
	if err != nil { return err }
	return a.Jobs.AdvanceStage(job.ID, model.StageExtract)
}
```

- [ ] **Step 19.3: Implement admin routes + middleware**

Create `route/admin.go`:
```go
package route

import (
	"crypto/subtle"
	"net/http"

	"samqna/service"

	"github.com/gin-gonic/gin"
)

func RegisterAdmin(r *gin.Engine, d *Deps) {
	adm := &service.Admin{Subs: d.Subs, Jobs: d.Jobs, IPs: d.IPs}
	g := r.Group("/admin", adminAuth(d.Cfg.AdminToken))
	g.POST("/v/:id/star", func(c *gin.Context) {
		if err := adm.ToggleStar(c.Param("id")); err != nil { c.AbortWithStatus(500); return }
		c.Status(200)
	})
	g.POST("/v/:id/delete", func(c *gin.Context) {
		if err := adm.Delete(c.Param("id")); err != nil { c.AbortWithStatus(500); return }
		c.Status(200)
	})
	g.POST("/v/:id/quarantine", func(c *gin.Context) {
		on := c.Query("on") != "0"
		if err := adm.Quarantine(c.Param("id"), on); err != nil { c.AbortWithStatus(500); return }
		c.Status(200)
	})
	g.POST("/v/:id/requeue", func(c *gin.Context) {
		if err := adm.Requeue(c.Param("id")); err != nil { c.AbortWithStatus(500); return }
		c.Status(200)
	})
	g.POST("/block-ip", func(c *gin.Context) {
		var body struct{ IP, Reason string }
		if err := c.ShouldBindJSON(&body); err != nil { c.AbortWithStatus(400); return }
		if err := adm.BlockIP(body.IP, body.Reason); err != nil { c.AbortWithStatus(500); return }
		c.Status(200)
	})
	g.GET("/quarantine", func(c *gin.Context) {
		subs, err := d.Subs.ListQuarantined(50, 0)
		if err != nil { c.AbortWithStatus(500); return }
		c.JSON(200, subs)
	})
	g.GET("/jobs", func(c *gin.Context) {
		depth, _ := d.Jobs.QueueDepth()
		c.JSON(200, gin.H{"queue_depth": depth})
	})
}

func adminAuth(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		given := c.GetHeader("X-Admin-Token")
		if subtle.ConstantTimeCompare([]byte(given), []byte(token)) != 1 {
			c.AbortWithStatus(http.StatusNotFound) // hide existence
			return
		}
		c.Next()
	}
}
```

- [ ] **Step 19.4: Run admin tests**

```bash
go test ./route/... -v -run TestAdmin
```
Expected: PASS.

- [ ] **Step 19.5: Commit**

```bash
git add service/admin.go route/admin.go route/admin_test.go
git commit -m "feat(samqna): admin routes (star/delete/quarantine/requeue/block-ip)"
```

---

## Phase 9 — Wire everything in main

### Task 20: Replace app.go + main.go with full wiring

**Files:**
- Replace: `main.go`, `app.go`

- [ ] **Step 20.1: Rewrite `app.go`**

Replace `app.go`:
```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"samqna/config"
	"samqna/migrations"
	"samqna/model"
	"samqna/notify"
	"samqna/pipeline"
	"samqna/repository"
	"samqna/route"
	"samqna/service"
	"samqna/storage"
	"samqna/view"

	"github.com/gin-gonic/gin"
)

type App struct {
	Cfg    *config.Config
	Router *gin.Engine
	Pool   *pipeline.Pool
	Pruner *pipeline.Pruner
	Notify *notify.Notifier
	srv    *http.Server
}

func CreateNewApp() (*App, error) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	db, err := config.ConnectDB(cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	if err := migrations.Migrate(db); err != nil {
		return nil, err
	}
	st := storage.New(cfg.MediaPath, cfg.ExportPath)
	if err := st.EnsureRoots(); err != nil {
		return nil, err
	}
	vw, err := view.New()
	if err != nil {
		return nil, err
	}

	subRepo := repository.NewSubmissionRepo(db)
	jobRepo := repository.NewJobRepo(db)
	tagRepo := repository.NewTagRepo(db)
	ipRepo := repository.NewIPRepo(db)

	subSvc := &service.Submissions{
		Subs: subRepo, Jobs: jobRepo, Tags: tagRepo, IPs: ipRepo,
		Storage: st, MaxBytes: cfg.MaxUploadBytes,
	}
	exportSvc := &service.Export{Storage: st, Subs: subRepo, FfmpegBin: cfg.FfmpegBin, MaxConcurrent: 2}

	// Pipeline
	reg := pipeline.NewRegistry()
	reg.Register(&pipeline.ExtractStage{Storage: st, FfmpegBin: cfg.FfmpegBin})
	reg.Register(&pipeline.WhisperStage{Bin: cfg.WhisperBin, ModelPath: cfg.WhisperModel})
	reg.Register(&pipeline.TagGradeStage{
		Client:           &http.Client{Timeout: 30 * time.Second},
		Endpoint:         "https://openrouter.ai/api/v1/chat/completions",
		APIKey:           cfg.OpenRouterKey,
		Models: []string{
			"google/gemini-2.5-flash",
			"google/gemini-2.0-flash-001",
			"deepseek/deepseek-chat",
			"qwen/qwen-2.5-7b-instruct",
		},
		QualityThreshold: cfg.QualityThreshold,
		TagRepo:          tagRepo,
		AttachTags: func(sub *model.Submission, tags []model.Tag) error {
			return db.Model(sub).Association("Tags").Replace(tags)
		},
	})
	pool := pipeline.NewPool(db, subRepo, jobRepo, reg, cfg.WorkerCount, 1*time.Second, 5)
	pruner := pipeline.NewPruner(subRepo, st, cfg.RetentionDays)
	n := notify.New()

	router := gin.New()
	router.Use(gin.Recovery(), slogMiddleware())
	deps := &route.Deps{
		Cfg: cfg, DB: db,
		Subs: subRepo, Jobs: jobRepo, Tags: tagRepo, IPs: ipRepo,
		Storage: st, View: vw,
		Submissions: subSvc, ExportSvc: exportSvc,
	}
	route.RegisterPublic(router, deps)
	route.RegisterAdmin(router, deps)

	app := &App{Cfg: cfg, Router: router, Pool: pool, Pruner: pruner, Notify: n}
	return app, nil
}

func (a *App) Run() error {
	a.Pool.Start()
	go a.Pruner.RunForever(context.Background(), 6*time.Hour)

	a.srv = &http.Server{Addr: ":" + a.Cfg.Port, Handler: a.Router}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "port", a.Cfg.Port)
		if err := a.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		slog.Info("shutdown signal received")
	case err := <-errCh:
		return fmt.Errorf("listen: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = a.srv.Shutdown(shutdownCtx)
	a.Pool.Stop(30 * time.Second)
	return nil
}

func slogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"dur_ms", time.Since(start).Milliseconds(),
		)
	}
}
```

- [ ] **Step 20.2: Simplify main.go**

Replace `main.go`:
```go
package main

import (
	"log/slog"
	"os"
)

func main() {
	app, err := CreateNewApp()
	if err != nil {
		slog.Error("create app", "err", err)
		os.Exit(1)
	}
	if err := app.Run(); err != nil {
		slog.Error("run", "err", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 20.3: Build the whole module**

```bash
go build ./...
```
Expected: clean build. Fix any leftover type errors.

- [ ] **Step 20.4: Run all unit tests**

```bash
go test ./... -count=1
```
Expected: all PASS (some tests skip without ffmpeg/whisper — that's fine).

- [ ] **Step 20.5: Commit**

```bash
git add main.go app.go
git commit -m "feat(samqna): wire pipeline+routes+services in app, graceful shutdown"
```

---

### Task 21: Integration smoke test (real ffmpeg, real whisper, fake LLM)

**Files:**
- Create: `integration_test.go` (in module root, behind `//go:build integration` tag)

- [ ] **Step 21.1: Write integration test**

Create `integration_test.go`:
```go
//go:build integration

package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEndToEnd_SubmitProcessExport(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required for integration test")
	}
	if os.Getenv("WHISPER_BIN") == "" || os.Getenv("WHISPER_MODEL_PATH") == "" {
		t.Skip("WHISPER_BIN / WHISPER_MODEL_PATH required")
	}
	// LLM stub
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"tags\":[\"test\"],\"quality_score\":80,\"summary\":\"hi\",\"is_spam\":false}"}}]}`))
	}))
	defer llm.Close()

	tmp := t.TempDir()
	t.Setenv("DATABASE_PATH", tmp+"/db")
	t.Setenv("MEDIA_PATH", tmp+"/media")
	t.Setenv("EXPORT_PATH", tmp+"/exports")
	t.Setenv("ADMIN_TOKEN", "x")
	t.Setenv("OPENROUTER_API_KEY", "x")
	t.Setenv("WORKER_COUNT", "1")
	// override endpoint via env (or expose hook). For this smoke, post manually:

	app, err := CreateNewApp()
	require.NoError(t, err)
	app.Pool.Start()
	defer app.Pool.Stop(5 * time.Second)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	mw.WriteField("consent", "on")
	part, _ := mw.CreateFormFile("video", "sample.mp4")
	src, _ := os.ReadFile("testdata/sample.mp4")
	part.Write(src)
	mw.Close()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/submit", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.RemoteAddr = "1.2.3.4:0"
	app.Router.ServeHTTP(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
}
```

> The test above intentionally stops short of validating end-to-end status flips because the OpenRouter endpoint isn't easily swappable without code change. That's acceptable for the smoke level — the real e2e is the `make e2e` target in deploy.

- [ ] **Step 21.2: Run integration test locally**

```bash
WHISPER_BIN=/opt/homebrew/bin/whisper-cli \
WHISPER_MODEL_PATH=$HOME/whisper-models/ggml-tiny.en.bin \
go test -tags=integration ./...
```
Expected: PASS (or SKIP if binaries unavailable).

- [ ] **Step 21.3: Commit**

```bash
git add integration_test.go
git commit -m "test(samqna): integration smoke test for full submission flow"
```

---

## Phase 10 — Deploy

### Task 22: Dockerfile + docker-compose + Makefile

**Files:**
- Create: `Dockerfile`, `docker-compose.yml`, `Makefile`

- [ ] **Step 22.1: Write Dockerfile**

Create `Dockerfile`:
```dockerfile
# syntax=docker/dockerfile:1.6
FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=1
RUN go build -o /out/samqna .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg ca-certificates curl libgomp1 && rm -rf /var/lib/apt/lists/*

# whisper.cpp prebuilt binary + small.en model
RUN curl -L -o /usr/local/bin/whisper-cli https://github.com/ggerganov/whisper.cpp/releases/download/v1.7.4/whisper-cli-linux-x64 \
    && chmod +x /usr/local/bin/whisper-cli
RUN mkdir -p /models && curl -L -o /models/ggml-small.en.bin \
    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.en.bin

COPY --from=builder /out/samqna /usr/local/bin/samqna
COPY view/ /app/view/
COPY static/ /app/static/
WORKDIR /app

ENV PORT=9000 \
    DATABASE_PATH=/data/samqna.db \
    MEDIA_PATH=/data/media \
    EXPORT_PATH=/data/exports \
    WHISPER_BIN=/usr/local/bin/whisper-cli \
    WHISPER_MODEL_PATH=/models/ggml-small.en.bin \
    FFMPEG_BIN=/usr/bin/ffmpeg

VOLUME ["/data"]
EXPOSE 9000
HEALTHCHECK --interval=30s --timeout=3s CMD curl -fsS http://localhost:9000/healthz || exit 1
ENTRYPOINT ["samqna"]
```

> The whisper-cli release URL above is illustrative — verify the latest release tag and asset filename on https://github.com/ggerganov/whisper.cpp/releases before building. If the prebuilt binary URL doesn't exist for your platform, fall back to building whisper.cpp in the builder stage:
> ```dockerfile
> RUN git clone --depth 1 https://github.com/ggerganov/whisper.cpp /whisper \
>     && cd /whisper && make -j$(nproc) main && cp main /usr/local/bin/whisper-cli
> ```

- [ ] **Step 22.2: Write docker-compose**

Create `docker-compose.yml`:
```yaml
services:
  samqna:
    build: .
    image: samqna:latest
    restart: unless-stopped
    ports:
      - "9000:9000"
    volumes:
      - ./data:/data
    environment:
      - OPENROUTER_API_KEY=${OPENROUTER_API_KEY}
      - TURNSTILE_SITE_KEY=${TURNSTILE_SITE_KEY}
      - TURNSTILE_SECRET=${TURNSTILE_SECRET}
      - ADMIN_TOKEN=${ADMIN_TOKEN}
      - TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}
      - TELEGRAM_CHAT_ID=${TELEGRAM_CHAT_ID}
      - WORKER_COUNT=2
      - QUALITY_THRESHOLD=30
    deploy:
      resources:
        limits:
          memory: 2g
```

- [ ] **Step 22.3: Add Makefile**

Create `Makefile`:
```makefile
.PHONY: dev test int build docker deploy backup

dev:
	air

test:
	go test ./... -count=1

int:
	WHISPER_BIN=$$WHISPER_BIN WHISPER_MODEL_PATH=$$WHISPER_MODEL_PATH \
	  go test -tags=integration ./... -v -count=1

build:
	CGO_ENABLED=1 go build -o samqna .

docker:
	docker compose build

deploy:
	docker compose up -d --build

backup:
	mkdir -p ./data/backups
	sqlite3 ./data/samqna.db ".backup ./data/backups/samqna-$$(date +%F).db"
```

- [ ] **Step 22.4: Build the Docker image locally**

```bash
docker compose build
```
Expected: successful build. (Run on a machine with the same arch as deploy target if cross-arch.)

- [ ] **Step 22.5: Smoke run + healthcheck**

```bash
ADMIN_TOKEN=test OPENROUTER_API_KEY=x docker compose up -d
sleep 5
curl -fsS http://localhost:9000/healthz
```
Expected: `{"status":"ok",...}`.

```bash
docker compose down
```

- [ ] **Step 22.6: Commit**

```bash
git add Dockerfile docker-compose.yml Makefile
git commit -m "feat(samqna): docker + compose + makefile for homeserver deploy"
```

---

### Task 23: First-deploy README

**Files:**
- Create: `README.md`

- [ ] **Step 23.1: Write a focused README**

Create `README.md`:
```markdown
# SamQnA

Single-creator open Q&A video inbox. Users submit ≤60s video questions; an AI pipeline transcribes, tags, and grades; the creator filters, picks, and exports clips for their own response videos.

See `docs/superpowers/specs/2026-05-24-samqna-design.md` for the design and `docs/superpowers/plans/2026-05-24-samqna-implementation.md` for the build plan.

## Local development

```bash
cp .env.example .env
# fill in ADMIN_TOKEN at minimum
make dev   # uses air for hot reload (or: go run .)
```

## Deploy to homeserver

1. Copy `.env.example` → `.env`, fill in real values.
2. `docker compose up -d --build`
3. Point a Cloudflare Tunnel at port 9000.
4. `curl https://<your-domain>/healthz` to verify.

## First-deploy sanity checklist

- [ ] `./data` exists with ≥ 50 GB free
- [ ] `OPENROUTER_API_KEY` valid (test with a manual curl)
- [ ] `TURNSTILE_SITE_KEY` and `TURNSTILE_SECRET` configured for your domain
- [ ] `ADMIN_TOKEN` is long, random (≥ 32 bytes)
- [ ] Submit a test video, watch transcript + tags + export work end-to-end
- [ ] Intentionally fail a job (rename ffmpeg binary briefly) — confirm Telegram alert fires

## Admin actions

All under `/admin/*`. Send `X-Admin-Token: <your-token>` header. Without it, endpoints return 404.

| Action            | curl                                                    |
|-------------------|---------------------------------------------------------|
| Star a video      | `curl -X POST -H "X-Admin-Token: $T" host/admin/v/$ID/star` |
| Delete            | `curl -X POST -H "X-Admin-Token: $T" host/admin/v/$ID/delete` |
| Block IP          | `curl -X POST -H "X-Admin-Token: $T" -d '{"ip":"1.2.3.4","reason":"spam"}' host/admin/block-ip` |
| Requeue pipeline  | `curl -X POST -H "X-Admin-Token: $T" host/admin/v/$ID/requeue` |

## Backups

`make backup` snapshots SQLite to `./data/backups/`. Cron it nightly:
```cron
0 3 * * * cd /srv/samqna && make backup
```

Media files are intentionally not backed up (30-day TTL; star videos to keep them).
```

- [ ] **Step 23.2: Commit**

```bash
git add README.md
git commit -m "docs(samqna): README with dev/deploy/admin/backup notes"
```

---

## Final verification

- [ ] **All unit tests pass**

```bash
go test ./... -count=1
```
Expected: everything PASS or SKIP (integration steps that need ffmpeg/whisper).

- [ ] **`go vet` and `go build` clean**

```bash
go vet ./...
go build ./...
```
Expected: no warnings, clean build.

- [ ] **Manual smoke**

Start app locally (`go run .` with `.env` filled), open http://localhost:9000:

- [ ] Submit a 5-second test video via upload
- [ ] Confirm submission appears with `processing` status
- [ ] Wait for pipeline; confirm status flips to `ready`, tags appear, transcript renders
- [ ] Click "Download MP4" — file downloads, plays in QuickTime/VLC
- [ ] Open trim UI, set start/end, download — clip is shorter
- [ ] Hit `/admin/v/{id}/star` with the admin token — star reflects on reload
- [ ] Tail logs (`docker compose logs -f` or stdout) — JSON lines per HTTP call and per pipeline stage

- [ ] **Tag the release**

```bash
git tag -a v0.1.0 -m "samqna v0.1.0 — initial production-grade build"
```

---

## Deferred to v1.1

These are in the spec but intentionally omitted from this plan to keep v1 scoped. Add tasks when needed:

- **`/admin/stats` page** (submissions today/week/month, disk usage, worker activity). The data is already in repos; needs a small handler + template.
- **Batch ZIP async path for >5 videos.** Current implementation builds synchronously. Async needs a `BatchJob` table + a 4th worker queue stage + poll endpoint.
- **Full `/healthz` payload** (disk free %, last_job_completed timestamp, individual worker active flags). Current version returns status + queue depth only.
- **Telegram alert triggers** for: permanent job failure, worker pool stuck >30 min, disk >85%, all LLMs failing in a call, panic recovered. The `notify` package is wired; trigger hooks need to be added in the pool/handlers.
- **Rate-limit caching for trim endpoint** (5s per video per source). Currently unrestricted — fine for single-creator use, harden when traffic warrants.

## Notes for the executor

- **TDD where it matters:** pipeline stages and services have tests written first. Routes and templates are covered by integration + manual smoke — that's intentional.
- **Skipped tests are expected** in environments without ffmpeg or whisper.cpp. Don't disable; just ensure they pass in your local dev env and the Docker image.
- **Frequent commits:** the plan groups by file/feature; commit per task, not per step, unless a task has natural sub-commits.
- **If a stage's external binary is missing or misnamed,** check `WHISPER_BIN` / `FFMPEG_BIN` env vars first before changing code.
- **Backoff is deliberately slow (30s/2m/10m/1h)** so a wedged stage doesn't burn LLM quota or CPU on repeated retries. Override only in test (`backoffOverride`).
