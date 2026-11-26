# ClipForge

ClipForge is a self-hosted clipping studio for **YouTube and Twitch**. You paste a URL, it transcribes the video locally with whisper.cpp, and you pick moments by hand or let Claude or Gemini suggest highlights. You can fix caption text, then render 9:16, 1:1 or 16:9 clips with burned-in animated captions plus an SRT file. Finished clips can be pushed to **Postiz** for scheduling.

It runs on an Oracle Cloud Ampere (arm64) VM, deployed in one of two ways:
- **Portainer + CloudPanel, reusing existing Postgres/Redis containers** with prebuilt GHCR images. See [docs/DEPLOY-PORTAINER.md](docs/DEPLOY-PORTAINER.md).
- **Standalone Docker Compose**, which bundles Postgres, Redis and Caddy. See [docs/DEPLOY-ORACLE.md](docs/DEPLOY-ORACLE.md).

## How it works

| Step | What happens |
|---|---|
| **Import** | `yt-dlp` reads metadata and downloads a small 360p **editor proxy** only, not the full-quality VOD. ffmpeg extracts 16 kHz audio. |
| **…or upload** | Drop a video file (MP4, MOV, MKV, WEBM…) on the **Upload file** tab. It's sent in resumable 8 MB chunks; H.264 MP4s are used as the preview directly, so long files are editable within seconds. |
| **Transcribe** | `whisper.cpp` (local, word timestamps) runs, with an optional OpenAI/Groq API fallback. Long VODs are chunked, with live progress. |
| **Pick clips** | Click words to seek and drag across the transcript to select. Mark In/Out with `I`/`O`. You can also use **Suggest clips with AI** (Claude or Gemini reads the timestamped transcript and returns scored, non-overlapping clips snapped to word boundaries). When an AI key is configured this runs automatically after transcription (toggle in Settings); re-run it any time from the **AI clips** button. |
| **Edit** | Trim handles, a draggable 9:16 reframe box, the caption style, and per-clip word fixes, all with a live caption preview on the player. |
| **Render** | yt-dlp resolves direct stream URLs and ffmpeg seeks precisely, encoding **only that range** at up to 1080p. The cover crop and libass ASS captions (highlight / pop / karaoke) are applied, plus loudness normalisation, H.264/AAC and faststart. If streaming fails, the fallback is `yt-dlp --download-sections`. |
| **Publish** | Upload to Postiz, then create a post (now, scheduled, or draft) on the selected channels. |

## Stack

- **api/**: Go 1.26, Gin, pgx (PostgreSQL 17), asynq (Redis) job queues, goose migrations, Anthropic Go SDK
  - `cmd/api` is the REST + SSE server. `cmd/worker` runs the background jobs (separate queues for ingest, transcribe, render and default). `cmd/hashpw` generates the admin password hash.
- **web/**: Next.js 15 (App Router), React 19, Tailwind 4
- **deploy/**: `portainer-stack.yml` (uses existing DBs and GHCR images), `docker-compose.yml` (self-contained: postgres, redis, caddy), Caddyfile, model downloader
- **.github/workflows/images.yml**: tests, then builds linux/arm64 images and pushes them to GHCR

## Local development

```bash
# Postgres + Redis running locally, then:
cd api && go test ./... && go run ./cmd/api      # needs ADMIN_EMAIL, ADMIN_PASSWORD_HASH, SESSION_SECRET, COOKIE_SECURE=false
cd api && go run ./cmd/worker                    # needs yt-dlp, ffmpeg, whisper-cli on PATH and WHISPER_MODELS_DIR
cd web && npm install && npm run dev             # proxies /api to http://localhost:8080
```

All configuration is environment-driven; see `deploy/.env.example` and `api/internal/config/config.go`.

## Notes

- Single admin user (bcrypt hash in env). Sessions are HMAC-signed cookies, mutating requests require an `X-Requested-With` header (CSRF), and logins are rate-limited.
- URLs are allow-listed to YouTube and Twitch and passed to yt-dlp as argv after `--`, never through a shell.
- Clip only content you have the rights to use, and respect each platform's terms.
