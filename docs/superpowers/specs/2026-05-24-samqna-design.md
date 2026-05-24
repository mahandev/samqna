# SamQnA — Design Spec

**Date:** 2026-05-24
**Status:** Approved for implementation planning
**Author:** Dev Amarnani

---

## 1. Product summary

SamQnA is a single-creator open Q&A video inbox. Viewers submit short (≤60s) video questions to one creator. The creator browses, filters by AI-generated tags and quality scores, and exports selected clips (single, trimmed, or in batch) to splice into their own video responses elsewhere (editing software, livestream, etc.).

The system is intentionally a **pipeline, not a destination** — submissions are processed by an AI layer (transcribe → tag → quality-grade → spam-filter) and presented to the creator for triage and export. The creator never answers within the app.

### Key product decisions

- **Single creator, many anonymous submitters.** The creator is the only "author" the system knows about.
- **Public dashboard.** No login to view; submitters consent to public visibility on submission.
- **Tags, not branches.** AI assigns multiple lowercase tags per video; the creator filters by tag combinations.
- **Free for submitters**, defended by Cloudflare Turnstile + per-IP rate limit + AI auto-quarantine of spam/low-quality submissions.
- **Destructive actions** (delete, block, quarantine, edit tags, requeue) gated by an `ADMIN_TOKEN` request header. Reads and exports are fully open.

---

## 2. Hardware target and load envelope

- **Box:** i5 7th gen (4c/4t, AVX2), 8 GB RAM, homeserver behind Cloudflare Tunnel.
- **Sustained throughput:** ~1,700 transcribed jobs/day with 2 worker goroutines (CPU-bound on whisper.cpp `small.en`).
- **Ingest:** bandwidth-bound, not CPU-bound. Submissions queue durably in SQLite when workers are saturated; no upload is rejected for being "too busy."
- **Memory budget:** app + 2 workers ≈ 1.2 GB. Docker memory limit set to 2 GB hard cap.
- **Disk:** ~10 MB per 60s video × 30-day retention. Plan for ~500 GB at theoretical max throughput, typically far less.

---

## 3. Architectural approach

**Approach A: Monolith with in-process worker pool, SQLite-backed durable job queue.**

One Go binary. HTTP handlers accept uploads → write file to disk → insert `jobs` row → return. A bounded worker pool (2 goroutines by default) polls SQLite, advances each job through pipeline stages (extract → transcribe → tag_grade), updates state durably. Jobs survive restarts; crashed workers' locks expire and are reclaimed.

**Why Approach A over a split API+worker or external queue:** at this hardware and scale, processing throughput is identical across all three architectures (~1,700 jobs/day, CPU-bound). The split/external-queue variants only earn their complexity if the worker keeps crashing or you outgrow the box. Approach A is the simplest reliable option, and the worker code lifts out into a separate binary in an afternoon if ever needed (it lives behind a `Stage` interface from day one).

---

## 4. Stack

- **Language/runtime:** Go 1.26 (CGO enabled for SQLite).
- **HTTP:** `github.com/gin-gonic/gin`.
- **ORM:** `gorm.io/gorm` with `gorm.io/driver/sqlite` (mattn/go-sqlite3 underneath).
- **Templates:** `templ` (Go-native, type-safe).
- **Frontend interactivity:** HTMX + small vanilla JS files for browser recording and trim sliders. No JS toolchain, no SPA.
- **Transcription:** `whisper.cpp` (`small.en` model, ~466 MB, baked into Docker image). Invoked as subprocess.
- **Media:** `ffmpeg` (system binary) for audio extraction, thumbnail generation, transcoding, trimming.
- **LLM:** OpenRouter with explicit fallback chain (Gemini 2.5 Flash → Gemini 2.0 Flash → DeepSeek Chat → Qwen 2.5 7B Instruct). JSON-mode responses.
- **Spam gate:** Cloudflare Turnstile (free).
- **Notifications:** Telegram (existing `~/.claude/skills/telegram-notify` setup, shelled out).
- **Deploy:** Docker + docker-compose + Cloudflare Tunnel on homeserver.

---

## 5. Package layout

```
samqna/
├── main.go                  // wire everything, start HTTP + worker pool
├── app.go                   // App struct, Run()
├── config/
│   └── config.go            // env loading, DB connect, paths
├── model/                   // GORM models, no logic
│   ├── submission.go        // Submission, Tag, Job, BlockedIP
│   └── enums.go             // JobStatus, ExportFormat, etc.
├── repository/              // data access only, no business logic
│   ├── submission.go
│   ├── job.go
│   └── ip.go
├── service/                 // business logic, orchestrates repos + pipeline
│   ├── submission.go        // create, list, filter, star
│   ├── export.go            // one-click, trim, batch ZIP
│   └── admin.go             // delete, block, requeue
├── pipeline/                // the AI worker pipeline (the heart of the app)
│   ├── pool.go              // worker pool, semaphore, polling loop
│   ├── stages.go            // Stage interface + pipeline runner
│   ├── ffmpeg.go            // extract audio, generate thumbnail
│   ├── whisper.go           // whisper.cpp wrapper (subprocess)
│   ├── llm.go               // OpenRouter client w/ fallback chain
│   └── retention.go         // 30-day video pruner (cron-style goroutine)
├── route/
│   ├── routes.go            // mount everything on *gin.Engine
│   ├── public.go            // submit, list, view, export (open)
│   └── admin.go             // delete, block, requeue (gated by ADMIN_TOKEN)
├── view/                    // templ templates
│   ├── layout.templ
│   ├── submit.templ
│   ├── dashboard.templ
│   ├── video.templ
│   └── components/          // small reusable pieces (tag chip, video card)
├── static/                  // htmx.min.js, app.css, recorder.js, trim.js
├── storage/                 // disk layout helpers, path resolution
│   └── storage.go
├── notify/                  // telegram alert wrapper
│   └── telegram.go
├── migrations/              // versioned schema (GORM AutoMigrate + small runner)
├── testdata/                // fixture videos, fixture transcripts
└── Dockerfile + docker-compose.yml
```

**Boundary rules:**
- `repository` is the only layer that knows GORM exists.
- `pipeline` is the only layer that knows ffmpeg/whisper/OpenRouter exist; each stage implements the `Stage` interface so fakes plug in cleanly.
- `service` is the only layer `route` calls. Routes never touch `repository` or `pipeline` directly.
- `view` only receives plain DTO structs — no GORM models leak into templates.

---

## 6. Data model

### `submissions`

| Column              | Type            | Notes |
|---------------------|-----------------|-------|
| `id`                | TEXT (ULID)     | sortable, URL-safe |
| `created_at`        | DATETIME        | indexed |
| `submitter_email`   | TEXT NULL       | optional, for "notify when answered" |
| `submitter_ip`      | TEXT            | for rate limit + block |
| `original_filename` | TEXT            | |
| `duration_sec`      | INT             | filled after ffmpeg probe |
| `size_bytes`        | INT             | |
| `video_path`        | TEXT NULL       | nullable — cleared on day-30 prune |
| `audio_path`        | TEXT            | always retained |
| `thumbnail_path`    | TEXT            | first-frame JPG |
| `transcript`        | TEXT NULL       | filled after whisper |
| `quality_score`     | INT NULL        | 0–100 from LLM grader |
| `summary`           | TEXT NULL       | one-line LLM summary |
| `status`            | TEXT            | `processing`, `ready`, `quarantined`, `failed` |
| `starred`           | BOOL            | bypasses 30-day prune |
| `star_reason`       | TEXT NULL       | optional creator note |
| `pruned_at`         | DATETIME NULL   | when video file was dropped |
| `deleted_at`        | DATETIME NULL   | soft delete |

Indexes: `(status, created_at DESC)`, `(starred, created_at DESC)`, `(submitter_ip, created_at)`.

### `tags`

| Column       | Type           |
|--------------|----------------|
| `id`         | INTEGER PK     |
| `name`       | TEXT UNIQUE    |
| `created_at` | DATETIME       |

### `submission_tags`

Join table. `(submission_id, tag_id)` composite PK; index on `tag_id` for filter queries.

### `jobs`

Durable pipeline queue. One row per submission while processing; deleted on success.

| Column           | Type          | Notes |
|------------------|---------------|-------|
| `id`             | INTEGER PK    | |
| `submission_id`  | TEXT FK       | unique |
| `stage`          | TEXT          | `extract`, `transcribe`, `tag_grade`, `done` |
| `status`         | TEXT          | `pending`, `running`, `failed` |
| `attempts`       | INT           | |
| `last_error`     | TEXT NULL     | |
| `locked_by`      | TEXT NULL     | worker ID |
| `locked_at`      | DATETIME NULL | stale lock → reclaim |
| `next_run_at`    | DATETIME      | for backoff |
| `created_at`     | DATETIME      | |
| `updated_at`     | DATETIME      | |

Worker claim: `UPDATE jobs SET locked_by=?, locked_at=now, status='running' WHERE id = (SELECT id FROM jobs WHERE status='pending' AND next_run_at <= now ORDER BY id LIMIT 1) RETURNING *`. A reaper goroutine resets locks older than 10 minutes back to `pending`.

### `blocked_ips`

| Column       | Type     |
|--------------|----------|
| `ip`         | TEXT PK  |
| `reason`     | TEXT     |
| `blocked_at` | DATETIME |

### Disk layout

```
data/
├── samqna.db
├── backups/                 // nightly sqlite .backup snapshots, last 14
├── exports/                 // cached one-click exports + batch ZIPs (TTL 24h for ZIPs)
└── media/
    └── {yyyy}/{mm}/{dd}/{ulid}/
        ├── original.mp4     // or original.webm from browser recorder
        ├── audio.opus       // 16 kHz mono, ~32 kbps — whisper-friendly
        └── thumb.jpg
```

Date-sharded so directories don't grow unbounded. Paths reconstructable from `created_at + id` if a DB row gets out of sync.

---

## 7. Pipeline state machine

### Stages

```
upload accepted (HTTP)
        │
        ▼
   [insert submission row: status=processing]
   [insert job row:        stage=extract, status=pending]
        │
        ▼
   ┌─────────────────┐
   │ STAGE: extract  │  ffmpeg: original → audio.opus + thumb.jpg, probe duration
   └─────────────────┘
        │ success → next_stage(transcribe)
        ▼
   ┌──────────────────┐
   │ STAGE: transcribe│  whisper.cpp small.en on audio.opus → transcript text
   └──────────────────┘
        │ success → next_stage(tag_grade)
        ▼
   ┌──────────────────┐
   │ STAGE: tag_grade │  OpenRouter → {tags[], quality_score, summary, is_spam}
   └──────────────────┘
        │ success
        ▼
   [update submission: status=ready (or quarantined if is_spam || score<threshold)]
   [delete job row]
```

### Stage interface

```go
type Stage interface {
    Name() string                                  // "extract" | "transcribe" | "tag_grade"
    Run(ctx context.Context, s *model.Submission) error
    Next() string                                  // returns next stage name or "" if terminal
}
```

The pipeline runner is intentionally dumb: load submission, look up stage by name in a registry, call `Run`, advance `job.stage = Next()` on success. On failure: bump `attempts`, record `last_error`, set `next_run_at = now + backoff(attempts)`.

### Worker pool

```go
type Pool struct {
    db      *gorm.DB
    workers int
    stages  map[string]Stage
    stop    chan struct{}
}
```

- N goroutines, each running `claim → run → release` in a loop.
- `workers` is configurable via `WORKER_COUNT` env var (default 2).
- Graceful shutdown on SIGTERM: close `stop`, release in-flight locks back to `pending`, wait up to 30s for stages to finish.

### Retry and backoff

| Attempt | Delay before next |
|---------|-------------------|
| 1 → 2   | 30 s              |
| 2 → 3   | 2 min             |
| 3 → 4   | 10 min            |
| 4 → 5   | 1 hr              |
| 5+      | permanent fail → `submission.status = failed`, Telegram alert |

`max_attempts = 5` per stage, configurable per-stage.

### Crash recovery

- On startup, reaper resets `jobs WHERE locked_at < now - 10min AND status = 'running'` to `pending` and increments attempts.
- All stages are idempotent: re-running `extract` overwrites `audio.opus`, re-running `transcribe` overwrites the transcript, etc. No partial-state corruption.

### LLM fallback chain (inside `tag_grade`)

```go
chain := []string{
    "google/gemini-2.5-flash",
    "google/gemini-2.0-flash-001",
    "deepseek/deepseek-chat",
    "qwen/qwen-2.5-7b-instruct",
}
```

Per call: 10s timeout, 1 retry, then fall through to the next model. If all four fail, the stage fails and the standard retry/backoff schedule applies.

### Grader prompt output

Single LLM call returns JSON (schema enforced via OpenRouter JSON mode):

```json
{
  "tags": ["career", "ai", "first-job"],
  "quality_score": 78,
  "summary": "Asking how to land a first AI/ML job without a relevant degree.",
  "is_spam": false,
  "spam_reason": null
}
```

Tags are lowercased and canonicalized (strip punctuation, fold near-duplicates like `AI` → `ai`) against the existing `tags` table before insert.

Quarantine rule: `is_spam == true` OR `quality_score < 30` (threshold configurable via env var). Quarantined submissions are hidden from the public list and only visible via admin routes.

### Retention pruner

Separate background goroutine, runs every 6 hours:

```sql
SELECT id, video_path, audio_path FROM submissions
WHERE starred = false
  AND pruned_at IS NULL
  AND created_at < datetime('now', '-30 days');
```

For each: delete `original.*` and any cached `data/exports/{id}.mp4`, set `video_path = NULL`, `pruned_at = now`. Audio + transcript + thumbnail remain.

---

## 8. HTTP / HTMX surface

### Open routes

| Method | Path                      | Purpose                                                       | Returns       |
|--------|---------------------------|---------------------------------------------------------------|---------------|
| GET    | `/`                       | Landing page                                                  | full page     |
| GET    | `/submit`                 | Upload form + record button + Turnstile + consent checkbox    | full page     |
| POST   | `/submit`                 | Multipart upload OR recorded blob → create submission + job   | redirect      |
| GET    | `/browse`                 | Dashboard: tag chips, quality slider, starred toggle, paged   | full page     |
| GET    | `/browse/list`            | List fragment (HTMX target for filter changes)                | HTML fragment |
| GET    | `/v/{id}`                 | Single video page: player, transcript, tags, score, exports   | full page     |
| GET    | `/v/{id}/status`          | Status badge fragment (polled while `processing`)             | HTML fragment |
| GET    | `/v/{id}/video`           | Stream video with Range support                               | video/mp4     |
| GET    | `/v/{id}/audio`           | Stream audio (after video pruned)                             | audio/opus    |
| GET    | `/v/{id}/thumb`           | Thumbnail                                                     | image/jpeg    |
| GET    | `/tags`                   | All tags with counts                                          | JSON          |
| GET    | `/healthz`                | DB ping + worker pool status                                  | JSON          |

### Export routes (open)

| Method | Path                            | Purpose                                                                |
|--------|---------------------------------|------------------------------------------------------------------------|
| GET    | `/v/{id}/export`                | One-click: re-mux or transcode to MP4/H.264/AAC, cache, stream         |
| POST   | `/v/{id}/export/trim`           | Body `{start, end}`: ffmpeg cuts segment, streams MP4 back             |
| POST   | `/export/batch`                 | Body `{ids: [...]}`: ZIP with clips + `manifest.json`; streamed        |
| GET    | `/export/batch/{job_id}`        | Poll for large (>5 video) batch builds                                 |

### Admin routes (require `X-Admin-Token` header; otherwise 404 — not 401)

| Method | Path                              | Purpose                              |
|--------|-----------------------------------|--------------------------------------|
| POST   | `/admin/v/{id}/delete`            | Soft delete                          |
| POST   | `/admin/v/{id}/star`              | Toggle starred                       |
| POST   | `/admin/v/{id}/tags`              | Edit tags (override AI)              |
| POST   | `/admin/v/{id}/quarantine`        | Move to/from quarantine              |
| POST   | `/admin/v/{id}/requeue`           | Re-run pipeline (job back to pending)|
| POST   | `/admin/block-ip`                 | Block IP                             |
| GET    | `/admin/quarantine`               | List quarantined submissions         |
| GET    | `/admin/jobs`                     | Queue depth, failures, attempts      |
| GET    | `/admin/stats`                    | Submissions volume, disk, workers    |

`ADMIN_TOKEN` checked in constant time via `crypto/subtle.ConstantTimeCompare`.

### HTMX patterns (the three places it earns its keep)

1. **Filter the browse list.** Tag chips and quality slider use `hx-get="/browse/list"`, `hx-trigger="change"`, `hx-target="#list"`, `hx-push-url="true"`. Server re-runs the query and swaps the list HTML.
2. **Live job status on the video page.** While `submission.status == processing`, page polls `/v/{id}/status` every 2s. When status flips to `ready`, server returns a fragment with `hx-swap-oob` that removes the poller and reveals the full UI.
3. **Star toggle and tag edits.** Per-action `hx-post`, swap just the icon or the tag list. Zero JS.

### Vanilla JS (only where HTMX can't help)

- `static/recorder.js` (~80 lines): `getUserMedia` → `MediaRecorder` with 60s hard cap → `Blob` → `FormData` POST → progress bar.
- `static/trim.js` (~60 lines): two range inputs bound to `<video>.currentTime`, POSTs `{start, end}` to the trim endpoint, triggers download.

### Rate limiting (middleware on `POST /submit`)

In-memory `map[ip]rateState` guarded by a mutex. Default: 3 submissions per IP per 24h. Blocked IPs (from `blocked_ips`) return 403 immediately. Block list loaded at startup, invalidated on `/admin/block-ip` call.

### Turnstile validation

On `POST /submit`, validate `cf-turnstile-response` against Cloudflare's `siteverify` endpoint (3s timeout). If Turnstile is misconfigured or unreachable, fall back to rate-limit-only — don't reject legitimate users because Cloudflare hiccups. Log the fallback at warn level.

### Streaming uploads

Use `c.Request.MultipartReader()` and `io.Copy` to disk rather than `c.FormFile()`. With 60s/10MB clips this barely matters, but it means a rogue 5 GB upload is killed after the size cap is hit (not after buffering all 5 GB into RAM). Size cap: 50 MB (generous headroom over a 60s/720p clip).

---

## 9. Export internals

### Codec target

All exports produce **MP4 / H.264 / AAC**, 30 fps. Browser recordings (WebM/VP9/Opus) get transcoded; phone uploads typically only need a re-mux. One consistent output type for the creator's editing software.

### One-click export (`GET /v/{id}/export`)

Handler logic:

```
if cached_export exists                    → stream it (no ffmpeg)
elif source is MP4/H.264/AAC               → re-mux only (-c copy, ~1s)
else                                       → transcode to MP4/H.264/AAC
write to data/exports/{id}.mp4 → stream  → leave cached
```

Cache key = submission id (one canonical export per video). Cached file deleted at the same time as the source video (day 30, unless starred).

### Trim export (`POST /v/{id}/export/trim`)

```
ffmpeg -ss {start} -to {end} -i original.mp4 \
       -c:v libx264 -preset veryfast -crf 22 \
       -c:a aac -b:a 128k \
       -movflags +faststart \
       -f mp4 pipe:1
```

Streamed straight to the HTTP response — no temp file. Not cached (every trim is different). Rate-limited to one trim per video per 5s window to prevent slider-spam from spawning dozens of ffmpeg processes.

### Batch ZIP export (`POST /export/batch`)

For ≤ 5 videos: synchronous. Streamed ZIP via `archive/zip` writing directly to the response body. Layout:

```
samqna-export-2026-05-24/
├── manifest.json
├── 001-01H8XYZ.mp4
├── 002-01H8ABC.mp4
└── ...
```

`manifest.json` shape:

```json
{
  "exported_at": "2026-05-24T18:00:00Z",
  "videos": [
    {
      "filename": "001-01H8XYZ.mp4",
      "submission_id": "01H8XYZ...",
      "created_at": "2026-05-21T10:30:00Z",
      "duration_sec": 47,
      "transcript": "...",
      "summary": "...",
      "tags": ["career", "ai"],
      "quality_score": 78,
      "submitter_email": null
    }
  ]
}
```

For > 5 videos: kicked to a background job, returns a `job_id`. `GET /export/batch/{job_id}` returns either progress (HTMX polls every 2s) or the download link when ready. Built ZIPs cached for 24h then deleted.

### Concurrency safety

- Export ffmpeg invocations launched with `nice -n 10` so they don't starve transcription workers.
- Semaphore caps **concurrent exports at 2**, independent of transcription load. Excess requests queue with a short wait ("preparing…" shown via HTMX).
- One-click cached exports bypass the semaphore — pure file streaming.

### Disk hygiene

Export cleanup is part of the 6-hour retention sweeper:
- Single-export files: deleted when parent video is pruned (day 30, unstarred).
- Batch ZIPs: hard 24h TTL regardless of star state.

---

## 10. Error handling and observability

### Three error layers

- **Stage errors** (ffmpeg crash, whisper failure, all LLMs down): handled by pipeline runner → `attempts++`, `last_error` recorded, backoff. After max attempts → `status = failed` + Telegram alert. Submitter sees a friendly "we couldn't process this" page.
- **Request errors** (bad upload, oversize, Turnstile fail): typed error → middleware renders friendly message. 4xx logged at info, 5xx at error with full context.
- **System errors** (disk full, DB locked, ffmpeg missing): logged at error, surfaced via `/healthz`, alerted via Telegram. App stays up; workers pause polling until the underlying condition clears.

### Structured logging

`log/slog` with JSON handler. Every log line includes: `submission_id`, `job_id`, `stage`, `attempt`, `worker_id` (when relevant). No `fmt.Println` anywhere.

### Telegram alerts (signal, not noise)

Reuses the existing `~/.claude/skills/telegram-notify/scripts/send.sh` pattern via a `notify` package wrapper. Alerts fire on:

- Permanent job failure (`attempts >= max`).
- Worker pool stuck (no job progress for >30 min while jobs pending).
- Disk usage >85%.
- All LLM fallbacks failing in a single call (suggests quota/cost issue).
- Any panic recovered in a handler or worker.

### `/healthz` payload

```json
{
  "status": "ok",
  "db": "ok",
  "workers": {"active": 2, "running": 1, "queue_depth": 4},
  "disk": {"free_gb": 142, "free_pct": 78},
  "last_job_completed": "2026-05-24T17:58:12Z"
}
```

200 if `db == ok && disk_pct < 95`.

---

## 11. Testing strategy

### Unit tests (per package, with fakes)

- `pipeline/stages.go` — each stage tested with fake ffmpeg/whisper/LLM. Cover success, retry, timeout, malformed LLM JSON, partial output.
- `service/*` — table-driven tests with in-memory SQLite. Whole suite under 1s.
- `repository/*` — covered transitively by service tests (avoid duplication).
- Tag canonicalization, rate limiter, retention pruner — small focused tests.

### Integration tests (gated by `-tags=integration`)

- Fixtures in `testdata/`: an MP4 phone upload, a WebM browser recording, an audio-only edge case, a corrupted file.
- Real `ffmpeg`, real `whisper.cpp` (using `tiny.en` for CI speed), faked LLM via `httptest` server returning canned JSON.
- One test per pipeline path: happy, ffmpeg-fail, whisper-fail, LLM-quarantine, retry-then-succeed, crash-recovery.

### End-to-end (manual, pre-deploy)

- `make e2e` target spins up Docker compose, posts a real video to `/submit`, polls until ready, asserts transcript + tags + export download.

### Coverage targets

- 70% line coverage on `service/` and `pipeline/`.
- No target elsewhere — glue code doesn't need coverage chasing.

### Explicitly NOT tested

- Template rendering correctness (manual smoke).
- HTMX swap behavior (manual smoke).
- ffmpeg/whisper output quality (quality concern, not correctness).
- Cloudflare Turnstile integration (covered by manual test).

---

## 12. Deployment

### Dockerfile (multi-stage)

```
Stage 1 (builder): golang:1.26 → CGO_ENABLED=1 → go build (sqlite needs cgo)
Stage 2 (runtime): debian:bookworm-slim
  apt: ffmpeg, ca-certificates
  COPY --from=builder /app/samqna /usr/local/bin/
  COPY whisper.cpp binary + small.en.bin model (baked into image)
  COPY static/ view/
  ENV PORT=9000
  VOLUME /data
  ENTRYPOINT ["/usr/local/bin/samqna"]
```

Whisper model (~466 MB) baked into the image — pulling 500 MB once is better than re-downloading per container restart.

### `docker-compose.yml`

```yaml
services:
  samqna:
    build: .
    restart: unless-stopped
    ports: ["9000:9000"]
    volumes:
      - /srv/samqna/data:/data
    environment:
      - DATABASE_PATH=/data/samqna.db
      - MEDIA_PATH=/data/media
      - EXPORT_PATH=/data/exports
      - OPENROUTER_API_KEY=${OPENROUTER_API_KEY}
      - TURNSTILE_SITE_KEY=${TURNSTILE_SITE_KEY}
      - TURNSTILE_SECRET=${TURNSTILE_SECRET}
      - ADMIN_TOKEN=${ADMIN_TOKEN}
      - TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}
      - TELEGRAM_CHAT_ID=${TELEGRAM_CHAT_ID}
      - WORKER_COUNT=2
      - WHISPER_MODEL=small.en
      - QUALITY_THRESHOLD=30
      - MAX_SUBMISSIONS_PER_IP_PER_DAY=3
    deploy:
      resources:
        limits:
          memory: 2g
```

### Cloudflare Tunnel

Tunnel from homeserver → `samqna.{domain}`. No port forwarding, no public IP exposure.

### Observability

- `docker logs samqna -f` for live tail.
- Docker `json-file` log driver, 10 MB × 3 files rotation.
- Telegram alerts as specified above.

### Backups

- SQLite: nightly `sqlite3 .backup` to `/data/backups/samqna-YYYY-MM-DD.db`, last 14 kept.
- Media: not backed up. Videos are ephemeral by design; starred ones can be re-downloaded by the creator on demand.

### First-deploy sanity checklist

- [ ] `/srv/samqna/data` exists with ≥ 50 GB free.
- [ ] OpenRouter key valid, credit balance non-zero.
- [ ] Turnstile site + secret keys configured for the domain.
- [ ] `ADMIN_TOKEN` is long and random (≥ 32 bytes).
- [ ] Tunnel routes correctly; `curl https://samqna.{domain}/healthz` returns 200.
- [ ] Submit one test video end-to-end, confirm transcript + tags + export work.
- [ ] Intentionally fail a job; confirm Telegram alert fires.

---

## 13. Out of scope (explicitly)

- **No author authentication.** Public dashboard, admin-token-gated mutations only. Login is a future addition if the product evolves past single-creator.
- **No in-app answer recording.** Creator answers in their own video, off-platform.
- **No multi-creator / multi-tenant support.** Schema is single-creator. Adding tenancy later is a non-trivial migration.
- **No notification-to-submitter when the creator answers.** Optional email is captured but not yet used; future enhancement.
- **No analytics dashboard for submitters.** No "how popular is my question" view.
- **No Postgres / horizontal scale support.** SQLite is the chosen ceiling for this build. Migration is possible later, not part of v1.
- **No CDN for media.** Cloudflare Tunnel proxies everything; if traffic grows past tunnel limits, that's a v2 concern.

---

## 14. Open questions for implementation phase

These don't change the design but need a decision during build:

- Exact wording of the grader prompt (iterate against real submissions).
- Default tag vocabulary (seed with empty, or pre-seed with creator-provided list?).
- Browser recorder MIME preference (`video/webm;codecs=vp9,opus` is widely supported; fall back to `video/mp4` on Safari).
- Whether to use `templ` or `html/template` (lean toward `templ` for type safety; `html/template` is fine if avoiding the codegen step is preferred).
