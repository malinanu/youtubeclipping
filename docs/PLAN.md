# ClipForge: self-hosted YouTube/Twitch clipping system

## Context
A self-hosted clipping tool for one admin user, running on an Oracle Cloud server. The flow is: paste a YouTube or Twitch URL, get a transcript, pick clips (by hand or from AI suggestions), add styled captions, render 9:16 or 16:9, then download or send to Postiz for scheduling. Reference projects: yt-dlp (ingest), ClipsAI (transcript-driven highlight picking and resizing), and Postiz (social scheduling). The repo `malinanu/youtubeclipping` is empty, so this is a greenfield build on branch `3logiq/quirky-galileo-fxgmxy`.

Decisions confirmed:
- Transcription runs locally with whisper.cpp, with an optional API fallback (OpenAI or Groq).
- Clips are picked by hand on a timeline, with Claude suggestions from the transcript.
- Outputs are 9:16 crop, burned-in styled captions, 16:9 with an SRT file, and a Postiz push.
- There is a single admin user.

## Architecture
```
Next.js 15 (web) ──► Go/Gin API ──► Postgres
                          │            ▲
                          ▼            │
                     Redis (asynq) ──► Go worker: yt-dlp · ffmpeg · whisper.cpp · Claude · Postiz
                                          │
                                   /data volume (media)  [R2 optional for finished clips]
```
All images are built for **linux/arm64**, since Oracle Ampere A1 is ARM. They run with Docker Compose, which Portainer can manage as a stack. CloudPanel or Cloudflare acts as the reverse proxy with TLS.

### Pipeline (asynq tasks)
1. **ingest**: run `yt-dlp -J` to get metadata (title, duration, chapters). Then download a **360p proxy MP4** for the editor and a **16 kHz mono WAV** for transcription. The full-quality source is not downloaded yet.
2. **transcribe**: run whisper.cpp (`small` or `medium` model, chosen in settings) with word-level timestamps and store the words. If the API fallback is enabled, it is used when the local run fails or the video is longer than N minutes. Twitch VODs and long streams are split into chunks with ffmpeg and transcribed in parallel up to the CPU limit.
3. **suggest** (on demand): send the transcript with segment timestamps to Claude (`claude-sonnet-5-5`, structured JSON output). It returns about 10 candidate clips, each with start, end, title, hook and score (30–90 s, configurable). These are saved as draft clips.
4. **render** (per clip): `yt-dlp --download-sections "*start-end" -f bv*+ba/b` fetches **only that range at full quality**, so long VODs are never pulled in full. Then ffmpeg crops to 9:16 (center plus a manual X offset, or the source 16:9), burns in ASS captions, normalizes loudness, and encodes H.264/AAC. Output files are the MP4, an SRT, and a thumbnail.
5. **publish** (optional): upload to the self-hosted Postiz through its Public API using an API key (`/public/v1/upload`, then `/public/v1/posts`) with the chosen integrations and schedule time. The exact payload will be checked against the running Postiz version when this is built.

Progress goes to Postgres and is pushed to the UI over SSE (`/api/jobs/:id/events`).

## Repo layout
```
/api            Go module (Gin): cmd/api, cmd/worker, internal/{config,db,auth,jobs,ytdlp,ffmpeg,whisper,captions,llm,postiz,storage,httpapi}
/api/migrations goose SQL migrations
/web            Next.js 15 App Router + Tailwind + shadcn/ui
/deploy         docker-compose.yml, .env.example, Caddyfile (optional), whisper model download script
/docs           PLAN.md, DEPLOY-ORACLE.md
CLAUDE.md
```

## Data model (Postgres)
- `videos`: id, source_url, platform (youtube|twitch), title, duration, status, proxy_path, audio_path, meta jsonb, error
- `transcripts`: video_id, language, provider, `words jsonb` (start, end, text, prob), `segments jsonb`
- `clips`: id, video_id, start_ms, end_ms, title, aspect (9:16|16:9), crop_x (0–1), caption_style_id, caption_overrides jsonb (edited words), status, output_path, srt_path, thumb_path, ai_score, ai_reason
- `caption_styles`: name, font, size, primary/highlight colours, outline, position, words_per_line, uppercase, animation (karaoke|pop|none)
- `jobs`: id, type, ref_id, status, progress, log, timestamps
- `publications`: clip_id, postiz_post_id, integrations, scheduled_at, status
- `settings`: a single row of JSON (whisper model, fallback provider, Claude key ref, Postiz URL)

## Key components
- **ytdlp pkg**: wraps the CLI with `exec.CommandContext`, parses `--progress-template` JSON lines, and supports `--cookies /data/cookies.txt`. URLs are validated against a YouTube/Twitch allowlist, and arguments are always passed as an argv array (never through a shell) so URLs cannot inject arguments. yt-dlp is pinned and self-updated by a nightly `yt-dlp -U` task, because extractors break often.
- **whisper pkg**: runs the `whisper-cli` binary, built from source in the worker image (ARM NEON, OpenBLAS), with `-ojf` full JSON for token timestamps. Tokens are merged into words. A `Provider` interface lets the OpenAI or Groq fallback plug in.
- **captions pkg**: builds an ASS file from words in the clip range (rebased to 0). It groups words by style rules and emits `\k` karaoke or per-word highlight events. An SRT is generated alongside.
- **ffmpeg pkg**: builds the filter graph, `crop=ih*9/16:ih:x,scale=1080:1920,subtitles=clip.ass:fontsdir=/fonts,loudnorm`. It uses `-preset veryfast` and a configurable thread cap. Fonts (Montserrat, Inter, Bebas, The Bold Font–style alternatives) are bundled.
- **llm pkg**: Anthropic Go SDK with a strict JSON schema prompt. Long transcripts are chunked by about 20 minutes and ranked across chunks.
- **postiz pkg**: an HTTP client with API key auth, `listIntegrations`, `upload` and `createPost`.
- **auth**: one admin set by `ADMIN_EMAIL` and `ADMIN_PASSWORD_HASH` (bcrypt) in env, an httpOnly secure session cookie, and login rate limiting.

## Web UI (Next.js)
- `/`: paste URL, plus a list of videos with their status.
- `/videos/[id]`: an editor with the proxy video player, a transcript panel (click a word to seek, drag-select text to create a clip), a timeline with clip ranges, and a "Suggest clips (AI)" button.
- Clip panel: trim in/out, aspect toggle, a crop X slider over the 9:16 preview, a caption style picker with a live CSS preview over the player, inline transcript word fixes, Render, Download MP4/SRT, and "Send to Postiz".
- `/styles`, `/settings`, `/login`.

## Deployment (Oracle)
- The compose services are `postgres:17`, `redis:7`, `api`, `worker` (yt-dlp, ffmpeg, whisper.cpp, deno for yt-dlp's JS challenges) and `web`. A shared `/data` volume holds media.
- `deploy/download-models.sh` fetches `ggml-small.bin` and `ggml-medium.bin`.
- Worker concurrency defaults to 1 render and 1 transcribe, tuned for 4 OCPU and 24 GB.
- A cleanup cron deletes proxy and source media after N days and keeps the finished clips.
- `docs/DEPLOY-ORACLE.md` covers the arm64 build, open ports in the VCN security list and iptables, the CloudPanel reverse proxy, and Postiz on the same host on its own subdomain.

## Risks to flag
- **YouTube blocks datacenter IPs** (Oracle) with a "Sign in to confirm you're not a bot" check. Mitigations: a cookies.txt upload in settings, the bgutil PO-token provider plugin as a sidecar, and an optional residential proxy setting. Without these, YouTube ingest will fail intermittently. Twitch is fine.
- whisper `medium` on ARM CPU is slow, roughly 0.5–1x realtime, so `small` is the default.
- Copyright and ToS are the user's responsibility. The tool is for content the user has rights to or for fair-use clipping.
- Face-tracked 9:16 reframing (as in ClipsAI) is **phase 2**. It would use OpenCV/MediaPipe in a Python sidecar. v1 uses a manual crop slider.

## Build phases
1. Scaffold the repo, compose, migrations, auth, and the ingest job with SSE progress.
2. whisper.cpp transcription and the transcript UI.
3. The clip editor and the render pipeline (crop, ASS captions, SRT).
4. Claude suggestions and the caption style presets.
5. The Postiz integration and the deploy docs.
6. Phase 2 backlog: face tracking, API transcription fallback UI, R2 offload, batch "auto mode".

## Verification
- `go test ./...` with unit tests for the ASS/SRT builder (golden files), the ffmpeg arg builder, the yt-dlp progress parser, and the LLM JSON parsing.
- `docker compose up` locally. Ingest a short public YouTube video and a Twitch clip, then check proxy, transcript, render 9:16 with captions, and the SRT. Use `ffprobe` to confirm 1080x1920 and the audio stream.
- Use Playwright to run login, paste URL, select a transcript range, render and download.
- Postiz: run a local Postiz container, create a draft post, and confirm it appears.
- Note: YouTube may block downloads from this cloud sandbox as well, so if YouTube fails, the e2e check falls back to Twitch or a local sample file.
