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
