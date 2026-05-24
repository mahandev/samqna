# SamSulekQnA

Single-creator open Q&A video inbox themed for Sam Sulek. Users submit ≤60s video questions; an AI pipeline transcribes, tags, and quality-grades (with a bodybuilding-weighted prompt); the creator browses the feed, manages from an admin dashboard, and exports clips for response videos.

Live: https://samsulekqna.xyz

See `docs/superpowers/specs/2026-05-24-samqna-design.md` for the design and `docs/superpowers/plans/2026-05-24-samqna-implementation.md` for the build plan.

## Local development

```bash
cp .env.example .env
# fill in ADMIN_TOKEN at minimum
make dev   # uses air for hot reload (or: go run .)
```

CF Access stays disabled when `CF_ACCESS_AUD` is empty — admin actions then only require the `X-Admin-Token` header + `X-Confirm: yes`.

## Deploy to homeserver

1. Copy `.env.example` → `.env`, fill in real values.
2. `docker compose up -d --build`
3. Point a Cloudflare Tunnel at port 9000.
4. `curl https://<your-domain>/healthz` to verify.

## Cloudflare Access for `/admin*`

Browser-friendly admin auth via Google (or email OTP). Free Cloudflare Zero Trust. ~3 min in the dashboard:

1. Cloudflare dashboard → **Zero Trust** → **Settings → General** → set a team domain (e.g. `samsulekqna.cloudflareaccess.com`).
2. **Settings → Authentication** → enable **Google** (paste OAuth client ID/secret) or **One-time PIN** (zero config — emails a 6-digit code).
3. **Access → Applications → Add** → "Self-hosted".
   - Application domain: `samsulekqna.xyz`, path: `/admin*`
   - Session: 24 hours
   - Add policy: "Allow if Email equals `your.email@gmail.com`"
   - Save and copy the **Application Audience (AUD) tag** from the application's overview.
4. Set the env vars in your `.env`:
   ```
   CF_ACCESS_TEAM_DOMAIN=samsulekqna.cloudflareaccess.com
   CF_ACCESS_AUD=<the AUD tag>
   ```
5. `docker compose up -d` to recreate the container.
6. Visit `https://samsulekqna.xyz/admin` from an incognito window → Cloudflare's login page → after you sign in with the allow-listed email, the dashboard renders.

The app also verifies the JWT itself for defense-in-depth — if Cloudflare were ever misconfigured, requests without a valid JWT signed for the configured AUD are rejected.

## First-deploy sanity checklist

- [ ] `./data` exists with ≥ 50 GB free
- [ ] `OPENROUTER_API_KEY` valid (Gemini Flash → DeepSeek → Qwen fallback chain)
- [ ] `TURNSTILE_SITE_KEY` and `TURNSTILE_SECRET` configured for your domain
- [ ] `ADMIN_TOKEN` is long, random (≥ 32 bytes) — used by scripts/curl
- [ ] `CF_ACCESS_TEAM_DOMAIN` + `CF_ACCESS_AUD` set if you want browser admin
- [ ] Submit a test video, watch transcript + tags + score appear live on the feed
- [ ] Pause the intake from /admin, confirm /submit returns 503, unpause again

## Admin actions

Two paths reach the same endpoints:

| Path        | Auth                                          | Used by                |
|-------------|-----------------------------------------------|------------------------|
| Browser     | Cloudflare Access cookie + verified JWT       | Inline buttons on cards, /admin dashboard |
| Script      | `X-Admin-Token: <token>` + `X-Confirm: yes`   | curl, cron, automation |

Either way, every destructive action is recorded in `admin_audits` and pinged to Telegram (if configured).

| Action            | curl |
|-------------------|------|
| Star a video      | `curl -X POST -H "X-Admin-Token: $T" -H "X-Confirm: yes" host/admin/v/$ID/star` |
| Delete (soft)     | `curl -X POST -H "X-Admin-Token: $T" -H "X-Confirm: yes" host/admin/v/$ID/delete` |
| Edit tags         | `curl -X POST -H "X-Admin-Token: $T" -H "X-Confirm: yes" -H "Content-Type: application/json" -d '{"tags":["hypertrophy","creatine"]}' host/admin/v/$ID/tags` |
| Block IP          | `curl -X POST -H "X-Admin-Token: $T" -H "X-Confirm: yes" -H "Content-Type: application/json" -d '{"ip":"1.2.3.4","reason":"spam"}' host/admin/block-ip` |
| Unblock IP        | `curl -X POST -H "X-Admin-Token: $T" -H "X-Confirm: yes" -H "Content-Type: application/json" -d '{"ip":"1.2.3.4"}' host/admin/unblock-ip` |
| Requeue pipeline  | `curl -X POST -H "X-Admin-Token: $T" -H "X-Confirm: yes" host/admin/v/$ID/requeue` |
| Pause intake      | `curl -X POST -H "X-Admin-Token: $T" -H "X-Confirm: yes" host/admin/pause` |
| Unpause intake    | `curl -X POST -H "X-Admin-Token: $T" -H "X-Confirm: yes" host/admin/unpause` |
| Recent audits     | `curl -H "X-Admin-Token: $T" host/admin/audit` |

## Backups

`make backup` snapshots SQLite to `./data/backups/`. Cron it nightly:
```cron
0 3 * * * cd /srv/samqna && make backup
```

Media files are intentionally not backed up (30-day TTL; star videos to keep them).
