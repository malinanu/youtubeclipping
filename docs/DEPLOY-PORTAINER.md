# Deploying ClipForge with Portainer + CloudPanel (existing Postgres & Redis)

This guide fits a server where **Portainer** manages Docker, **Postgres** and **Redis** already run as containers, and **CloudPanel** handles domains, SSL and nginx. ClipForge doesn't use CloudPanel's MySQL.

```
Internet ─TLS─► CloudPanel nginx (clips.example.com)
                   ├─ /api/*  ─► 127.0.0.1:18080   api  (Go, live progress via SSE)
                   └─ /       ─► 127.0.0.1:13000   web  (Next.js)
Portainer stack "clipforge":  api · worker · web · bgutil · redis (private job queue, not published)
   └─ via the Docker host gateway 172.17.0.1 ─► your postgres :5432 (database "clipforge")
```

Images are built for arm64 by GitHub Actions (`.github/workflows/images.yml`) and published to GHCR:
- `ghcr.io/malinanu/clipforge-backend`
- `ghcr.io/malinanu/clipforge-web`

Portainer only pulls them, so nothing is compiled on the server.

---

## 1. Postgres: create a database and role

Requires **PostgreSQL 13 or newer**. ClipForge needs no extensions or superuser rights.

In Portainer, go to **Containers → your postgres container → Console** (`/bin/sh`). Then run:

```bash
psql -U postgres   # or whatever superuser you set with POSTGRES_USER
```
```sql
CREATE ROLE clipforge LOGIN PASSWORD 'use-a-long-hex-password';
CREATE DATABASE clipforge OWNER clipforge;
\q
```

The tables are created automatically on first start, through embedded migrations.

## 2. How ClipForge reaches Postgres

The stack includes its **own small Redis** for the job queue. It isn't published, never evicts jobs, and needs no setup, so you don't have to connect your existing Redis. To use an external Redis anyway, set `REDIS_ADDR` (and `REDIS_PASSWORD`/`REDIS_DB`) in the stack env.

ClipForge reaches your Postgres through the **Docker host gateway**, `172.17.0.1`, at the port it publishes on the host. `host.docker.internal` also works and resolves to the same address.

> Don't use `localhost`. Inside a container, `localhost` is the container itself, not your server.

Check that both ports are published so other containers can reach them:

```bash
docker ps --format '{{.Names}}\t{{.Ports}}' | grep -E '5432|6379'
```

| What you see | Meaning |
|---|---|
| `0.0.0.0:5432->5432/tcp` or `172.17.0.1:5432->…` | ✅ Reachable at `172.17.0.1:5432` |
| `127.0.0.1:6379->6379/tcp` | ❌ Loopback only, so containers can't reach it. In Portainer, edit that container and change the port's host IP to `172.17.0.1`, then redeploy it. |
| no port shown | ❌ Not published. Publish it on `172.17.0.1`, or use the shared-network method below. |

**Security note:** a port published on `0.0.0.0` is open on every interface, and Docker bypasses ufw/iptables for published ports. A **passwordless Redis** on `0.0.0.0` is then protected only by Oracle's VCN security list. Make sure **5432 and 6379 are *not*** in your VCN ingress rules (Networking → VCN → Security Lists). Better still, publish them on `172.17.0.1` only, which is reachable from containers but not from the internet, and give Redis a password (`--requirepass`).

Quick connectivity test from a throwaway container:

```bash
docker run --rm --add-host=host.docker.internal:host-gateway redis:7-alpine redis-cli -h 172.17.0.1 -p 6379 ping
```
It should print `PONG`.

<details><summary>Alternative: shared Docker network (container names instead of IPs)</summary>

If you'd rather not publish ports, put Postgres, Redis and ClipForge on one user-defined network:

1. `docker network create shared-db`, then `docker network connect shared-db <postgres-container>` and `docker network connect shared-db <redis-container>`.
2. In the Portainer stack editor, add `networks: [default, shared-db]` to the `api` and `worker` services, plus a top-level `networks: { shared-db: { external: true } }`.
3. Use the container names in the URLs, for example `@postgres:5432` and `REDIS_ADDR=redis:6379`.
</details>

## 3. Redis checks (only if you point REDIS_ADDR at an external Redis)

ClipForge uses its own DB index (default `5`), so its keys never mix with other apps'. One setting matters on a shared Redis: if `maxmemory-policy` evicts keys, queued jobs can disappear under memory pressure. Check it:

```bash
docker exec <redis-container> redis-cli CONFIG GET maxmemory-policy
```
`noeviction` (the Redis default) is what you want. If you set a password with `--requirepass`, add `-a <password>` to the command above.

The worker also logs a warning at startup when the policy is anything else.

## 4. Whisper models: nothing to do

The `small` model (~470 MB) is **baked into the backend image**, so transcription works the moment the stack starts. Nothing is downloaded on the server.

If you pick a different model in **Settings → Transcription** (for example `medium` or `large-v3-turbo`), the worker downloads it automatically on first use into the `models` volume. The video shows *downloading model* progress while that happens. To bake different models into the image instead, change the `WHISPER_MODELS` build arg in `.github/workflows/images.yml`.

## 5. Images: let Portainer pull from GHCR

1. Push to `main`, or run **Actions → Build images → Run workflow**, and wait for it to finish. The first backend build takes the longest because it compiles whisper.cpp.
2. GHCR packages are **private by default**. Pick one:
   - Make them public: GitHub → your profile → **Packages** → `clipforge-backend` / `clipforge-web` → **Package settings** → Change visibility.
   - Or add credentials in Portainer: **Registries → Add registry → Custom registry**. Use URL `ghcr.io`, your GitHub username, and a **classic PAT with `read:packages`** as the password.

## 6. Admin password hash and secrets

```bash
docker run --rm ghcr.io/malinanu/clipforge-backend:latest hashpw 'a-long-unique-password'
```
```bash
openssl rand -base64 48
```

The first command outputs the value for `ADMIN_PASSWORD_HASH`. It's the bcrypt hash, base64-encoded so it contains no `$` and can be pasted into Portainer as-is. The second generates `SESSION_SECRET`.

## 7. Create the stack in Portainer

**Stacks → Add stack**:

- **Name:** `clipforge`
- **Build method:** *Repository*
  - Repository URL: `https://github.com/malinanu/youtubeclipping`. Turn on *Authentication* with a PAT if the repo is private.
  - Repository reference: `refs/heads/main`
  - Compose path: `deploy/portainer-stack.yml`
  - Optional: enable *GitOps updates* (polling) so stack-file changes apply automatically.
- **Environment variables**: use *Advanced mode* and paste the block below, then edit the values:

```env
DATABASE_URL=postgres://clipforge:<your-db-password>@172.17.0.1:5432/clipforge?sslmode=disable

ADMIN_EMAIL=you@example.com
ADMIN_PASSWORD_HASH=paste-hashpw-output-from-step-6
SESSION_SECRET=paste-openssl-output
COOKIE_SECURE=true

AI_PROVIDER=auto
ANTHROPIC_API_KEY=
CLAUDE_MODEL=claude-opus-5-5
GEMINI_API_KEY=
GEMINI_MODEL=gemini-3.5-flash-lite
OPENAI_API_KEY=
GROQ_API_KEY=
POSTIZ_API_URL=
POSTIZ_API_KEY=

API_PORT=18080
WEB_PORT=13000
CLIPFORGE_TAG=latest
WHISPER_THREADS=4
FFMPEG_THREADS=4
MEDIA_RETENTION_DAYS=14
```

Use a hex or alphanumeric DB password; symbols such as `@`, `/` and `#` break the URL unless they're percent-encoded.

Click **Deploy the stack**. Then check that the API is healthy:

```bash
curl -s http://127.0.0.1:18080/healthz
```

You can follow the worker in Portainer under **Containers → clipforge-worker-1 → Logs**.

If 18080 or 13000 is already in use on the host, change `API_PORT` and `WEB_PORT`, and use the same numbers in step 8.

## 8. Domain and SSL in CloudPanel

1. **Sites → Add Site → Create a Reverse Proxy**. Domain: `clips.example.com`. Reverse Proxy URL: `http://127.0.0.1:13000`.
2. Go to the site → **SSL/TLS → Actions → New Let's Encrypt Certificate**.
3. Go to the site → **Vhost**. Add this block **above** the existing `location / {` block, inside the `server { listen 443 … }` section, then save:

```nginx
  # ClipForge API: unbuffered so live progress (SSE) streams; long timeouts for big jobs.
  location /api/ {
    proxy_pass http://127.0.0.1:18080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    client_max_body_size 10m;   # uploads are sent in 8 MB chunks
    proxy_request_buffering off;
  }
```

Open `https://clips.example.com` and sign in with `ADMIN_EMAIL` and your password.

If the domain is proxied through Cloudflare (orange cloud), SSE still works. Cloudflare doesn't buffer `text/event-stream`.

## 9. AI clip suggestions: Claude or Gemini

"Suggest clips with AI" works with either provider. Pick one:

- **Gemini:** create a key at [Google AI Studio](https://aistudio.google.com) → **Get API key**.
- **Claude:** create a key at [console.anthropic.com](https://console.anthropic.com) → **API keys**.

There are two ways to add the key:

1. **In the app (recommended, no redeploy):** go to **Settings → AI clip suggestions**. Set **Provider** to *Gemini* (or *Claude*), paste the key, and click **Save settings**. It applies to the next suggestion run. The key is stored in ClipForge's database and shown masked.
2. **In the stack env:** set `GEMINI_API_KEY=…` (and optionally `AI_PROVIDER=gemini`) in Portainer, then **Update the stack**. Values saved in the Settings page override the env.

`AI_PROVIDER=auto` uses Claude when an Anthropic key is set, otherwise Gemini. The Gemini model defaults to `gemini-3.5-flash-lite` (fast and cheap). You can change it in Settings, for example to `gemini-3.8-flash` for higher quality.

## 9b. Uploading video files

Instead of pasting a URL, you can use **Upload file** on the home page. This suits long streams you've downloaded and pre-cut yourself, or videos YouTube won't let the server fetch.

- **Chunked and resumable:** files go up in 8 MB chunks, so the `client_max_body_size 10m` above is enough. If the connection drops, pick the same file again and it resumes where it stopped.
- **Fast preview:** H.264 MP4/MOV (and VP9 WebM) up to 1080p are used directly as the editor preview, so a 5-hour file is ready to scrub as soon as the audio is extracted. Other formats (HEVC, MKV, …) get a 360p preview transcoded first.
- **Storage:** uploaded originals are **kept until you delete the video**, because clips are rendered from them. Check disk use with `docker system df -v | grep clipforge_media`. The limit is `MAX_UPLOAD_GB` (default 20). Uploads abandoned mid-way are removed after 24 hours.

## 10. Updating

1. Push to `main`. GitHub Actions builds new `:latest` images.
2. In Portainer, go to **Stacks → clipforge → Pull and redeploy**, and tick *Re-pull image*.

To pin a version, set `CLIPFORGE_TAG=sha-abc1234` (any tag from the package page) and redeploy. Rolling back works the same way.

Database migrations run automatically on start and are safe to re-run.

## 11. Operations

- **Backups:** run `docker exec <postgres-container> pg_dump -U clipforge clipforge | gzip > clipforge-$(date +%F).sql.gz`. Rendered clips live in the `clipforge_media` volume.
- **Disk:** editor previews and audio are deleted after `MEDIA_RETENTION_DAYS`. Rendered clips are kept until you delete them in the UI. Check usage with `docker system df -v | grep clipforge`.
- **YouTube blocking** on Oracle IPs ("Sign in to confirm you're not a bot"): open **Settings → YouTube access**, follow the 4 incognito-export steps shown there, upload cookies.txt and click **Test YouTube access** until it shows ✅. Then Retry the video. If cookies alone don't work, set `YTDLP_PROXY` to a residential proxy. More detail in [DEPLOY-ORACLE.md §5](DEPLOY-ORACLE.md#5-youtube-from-a-datacenter-ip-important).
- **Postiz:** set `POSTIZ_API_URL` to `https://postiz.example.com/api/public/v1` plus the API key, either in the stack env or in the UI under Settings.
- **Transcription "killed by the system" (out of memory):** check with `free -h`, `docker stats --no-stream` and `sudo dmesg -T | grep -i -E "killed process|out of memory" | tail`. Progress is saved per 10-minute chunk, so **Retry resumes** where it stopped. To prevent it: add swap (see [DEPLOY-ORACLE.md §1](DEPLOY-ORACLE.md#1-server-prerequisites)), keep `HEAVY_JOBS=1` (transcription and renders take turns), use the `small.en`/`base` model, or set `WHISPER_THREADS=3`. Defaults are tuned for speed and memory: greedy decoding (`WHISPER_BEAM_SIZE=1`) and no context carry-over (`WHISPER_MAX_CONTEXT=0`, which also prevents repeated-phrase loops).
