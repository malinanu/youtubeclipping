# ClipForge — project notes for Claude Code

Self-hosted YouTube/Twitch clipping: import URL → whisper.cpp transcript → manual/AI clip picking → ffmpeg render with libass captions → Postiz publish. Single admin. Deployed with Docker Compose on Oracle Ampere A1 (arm64).

## Layout
- `api/` Go module `github.com/malinanu/youtubeclipping/api`
  - `cmd/api` (Gin REST + SSE), `cmd/worker` (asynq; one server per queue: ingest/transcribe/render/default), `cmd/hashpw`
  - `internal/ytdlp` CLI wrapper (argv only, URL allowlist, progress JSON parsing, direct stream resolve, section fallback)
  - `internal/ffmpeg` arg builder (`RenderArgs`/`VideoFilter` are pure + unit tested) and runners
  - `internal/captions` ASS (highlight/pop/karaoke/none) + SRT; `FontFamily` maps weight → "Montserrat Black" etc.
  - `internal/whisper` whisper.cpp (`-ml 1 -sow -oj`) + OpenAI-compatible API provider + fallback wrapper
  - `internal/llm` Claude clip suggestions (structured outputs, server-side fallbacks, word-boundary snapping)
  - `internal/postiz` Public API client; `internal/tasks` job handlers; `internal/store` SQL; `migrations/` goose (embedded)
- `web/` Next.js 15 App Router client UI; `/api/*` is same-origin (Caddy in prod, rewrite in dev)
- `deploy/portainer-stack.yml` (prod: existing Postgres/Redis via external network, GHCR images, CloudPanel nginx in front) and `deploy/docker-compose.yml` (self-contained); docs in `docs/DEPLOY-PORTAINER.md`, `docs/DEPLOY-ORACLE.md`
- `.github/workflows/images.yml` builds linux/arm64 images → `ghcr.io/malinanu/clipforge-{backend,web}`

## Conventions
- Times are integer milliseconds everywhere (DB, API, transcript words `{s,e,t}`).
- Stored media paths are relative to `DATA_DIR`; resolve with `media.Layout.Abs` (blocks traversal).
- Word overrides per clip are absolute-time words; `media.MergeOverrides` splices them over the transcript.
- Caption preview (`web/components/CaptionPreview.tsx#groupWords`) must mirror `captions.Group` in Go.
- Permanent job errors return `asynq.SkipRetry`; set user-visible status only on last attempt or permanent failure.
- Mutating API calls need header `X-Requested-With: clipforge`.
- Migrations must run as a non-superuser on PG 13+ (no `CREATE EXTENSION`).

## Commands
- `cd api && go test ./...` (render integration test runs if ffmpeg is installed)
- `cd web && npx tsc --noEmit && npm run build`
- `cd deploy && docker compose up -d --build`
