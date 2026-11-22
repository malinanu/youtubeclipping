# Deploying ClipForge with Portainer + CloudPanel (existing Postgres & Redis)

This guide fits a server where **Portainer** manages Docker, **Postgres** and **Redis** already run as containers, and **CloudPanel** handles domains, SSL and nginx. ClipForge doesn't use CloudPanel's MySQL.

```
Internet ─TLS─► CloudPanel nginx (clips.example.com)
                   ├─ /api/*  ─► 127.0.0.1:18080   api  (Go, live progress via SSE)
                   └─ /       ─► 127.0.0.1:13000   web  (Next.js)
Portainer stack "clipforge":  api · worker · web · bgutil
   └─ joins your existing Docker network ─► postgres (database "clipforge") · redis (DB index 5)
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

## 2. Find the shared Docker network

ClipForge reaches Postgres and Redis by container name over a Docker network. On the server:

```bash
docker ps --format '{{.Names}}' | grep -Ei 'postgres|redis'
```
```bash
docker inspect <postgres-container> -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}'
```
```bash
docker inspect <redis-container> -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}'
```

Pick a network both containers share and use it as `DB_NETWORK`. If they're only on the default `bridge` network, which has no DNS, create a shared network and attach them. This causes no downtime:

```bash
docker network create shared-db
```
```bash
docker network connect shared-db <postgres-container>
```
```bash
docker network connect shared-db <redis-container>
```

You can also do this in Portainer under **Networks → Add network**, then go to each container and use **Join network**.

## 3. Redis checks

ClipForge uses its own DB index (default `5`), so its keys never mix with other apps'. One setting matters on a shared Redis: if `maxmemory-policy` evicts keys, queued jobs can disappear under memory pressure. Check it:

```bash
docker exec <redis-container> redis-cli CONFIG GET maxmemory-policy
```
`noeviction` (the Redis default) is what you want. If you set a password with `--requirepass`, add `-a <password>` to the command above.

The worker also logs a warning at startup when the policy is anything else.

## 4. Whisper models on the host

```bash
sudo mkdir -p /opt/clipforge/models && sudo chown $USER /opt/clipforge/models
```
```bash
curl -fsSL https://raw.githubusercontent.com/malinanu/youtubeclipping/main/deploy/download-models.sh -o /tmp/dm.sh
```
```bash
MODELS_DIR=/opt/clipforge/models bash /tmp/dm.sh small
```

If the repo is private, copy `deploy/download-models.sh` onto the server with `scp` instead of curl. Other options are `small.en` (English only), `medium` and `large-v3-turbo`. See the speed table in [DEPLOY-ORACLE.md](DEPLOY-ORACLE.md#3-build-set-the-admin-password-download-models).

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
DB_NETWORK=shared-db
DATABASE_URL=postgres://clipforge:use-a-long-hex-password@postgres:5432/clipforge?sslmode=disable
REDIS_ADDR=redis:6379
REDIS_PASSWORD=
REDIS_DB=5

ADMIN_EMAIL=you@example.com
ADMIN_PASSWORD_HASH=paste-hashpw-output-from-step-6
SESSION_SECRET=paste-openssl-output
COOKIE_SECURE=true

ANTHROPIC_API_KEY=
CLAUDE_MODEL=claude-opus-5-5
OPENAI_API_KEY=
GROQ_API_KEY=
POSTIZ_API_URL=
POSTIZ_API_KEY=

MODELS_DIR=/opt/clipforge/models
API_PORT=18080
WEB_PORT=13000
CLIPFORGE_TAG=latest
WHISPER_THREADS=4
FFMPEG_THREADS=4
MEDIA_RETENTION_DAYS=14
```

Replace `postgres` and `redis` in the URLs with your **actual container names** from step 2. Use a hex or alphanumeric DB password; symbols such as `@`, `/` and `#` break the URL unless they're percent-encoded.

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
    client_max_body_size 10m;
  }
```

Open `https://clips.example.com` and sign in with `ADMIN_EMAIL` and your password.

If the domain is proxied through Cloudflare (orange cloud), SSE still works. Cloudflare doesn't buffer `text/event-stream`.

## 9. Updating

1. Push to `main`. GitHub Actions builds new `:latest` images.
2. In Portainer, go to **Stacks → clipforge → Pull and redeploy**, and tick *Re-pull image*.

To pin a version, set `CLIPFORGE_TAG=sha-abc1234` (any tag from the package page) and redeploy. Rolling back works the same way.

Database migrations run automatically on start and are safe to re-run.

## 10. Operations

- **Backups:** run `docker exec <postgres-container> pg_dump -U clipforge clipforge | gzip > clipforge-$(date +%F).sql.gz`. Rendered clips live in the `clipforge_media` volume.
- **Disk:** editor previews and audio are deleted after `MEDIA_RETENTION_DAYS`. Rendered clips are kept until you delete them in the UI. Check usage with `docker system df -v | grep clipforge`.
- **YouTube blocking** on Oracle IPs: upload cookies.txt in Settings, keep the `bgutil` service running, and optionally set `YTDLP_PROXY`. Details are in [DEPLOY-ORACLE.md §5](DEPLOY-ORACLE.md#5-youtube-from-a-datacenter-ip-important). yt-dlp updates itself nightly inside the worker, and a redeploy with a fresh image resets it to the version baked into the image.
- **Postiz:** set `POSTIZ_API_URL` to `https://postiz.example.com/api/public/v1` plus the API key, either in the stack env or in the UI under Settings.
