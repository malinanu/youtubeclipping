# Deploying ClipForge on Oracle Cloud

> Already running Portainer with your own Postgres/Redis containers and CloudPanel? Use
> [DEPLOY-PORTAINER.md](DEPLOY-PORTAINER.md) instead. This guide is the self-contained Compose setup.

Target: an **Ampere A1** VM (arm64) on Ubuntu 22.04/24.04, for example the Always Free 4 OCPU / 24 GB shape. Docker runs the stack. CloudPanel (or a Cloudflare Tunnel) terminates TLS in front of it.

```
Internet ─TLS─► CloudPanel nginx (clips.example.com) ─► 127.0.0.1:8088 Caddy ─┬─► web  (Next.js :3000)
                                                                              └─► api  (Go :8080, /api/*)
worker (yt-dlp · ffmpeg · whisper.cpp) ◄── Redis queue ──► api        Postgres · bgutil PO-token sidecar
```

## 1. Server prerequisites

```bash
sudo apt update && sudo apt install -y git curl
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER && newgrp docker
docker compose version
```

If Portainer manages this host you can deploy `deploy/docker-compose.yml` as a stack. The images build locally, so building from the CLI once (step 3) is simplest.

**Swap:** whisper `medium` and parallel renders can spike memory. 4 GB of swap is cheap insurance:

```bash
sudo fallocate -l 4G /swapfile && sudo chmod 600 /swapfile && sudo mkswap /swapfile && sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
```

## 2. Get the code and configure

```bash
git clone https://github.com/malinanu/youtubeclipping.git clipforge && cd clipforge/deploy
cp .env.example .env
sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$(openssl rand -hex 24)/" .env
sed -i "s/^REDIS_PASSWORD=.*/REDIS_PASSWORD=$(openssl rand -hex 24)/" .env
sed -i "s|^SESSION_SECRET=.*|SESSION_SECRET=$(openssl rand -base64 48 | tr -d '\n')|" .env
nano .env   # set ADMIN_EMAIL, ANTHROPIC_API_KEY, POSTIZ_* (optional)
```

## 3. Build, set the admin password, download models

```bash
docker compose build                    # first build compiles whisper.cpp natively for the Ampere CPU (~5-10 min)
docker compose run --rm --no-deps api hashpw 'a-long-unique-password'
# paste the output into .env as  ADMIN_PASSWORD_HASH=<output>   (base64, no quoting needed)

./download-models.sh small              # ~470 MB. Add medium or large-v3-turbo if you want higher accuracy.
docker compose up -d
docker compose ps
curl -s http://127.0.0.1:8088/healthz   # {"ok":true}
```

Speed guide for whisper.cpp on 4 Ampere OCPU (approximate):

| Model | Speed (audio:wall) | Notes |
|---|---|---|
| `base` / `base.en` | ~8–10x realtime | Fast drafts |
| `small` / `small.en` | ~3–4x realtime | **Default.** Good accuracy for English |
| `medium` | ~1x realtime | Better for accents and non-English |
| `large-v3-turbo` | ~1–1.5x realtime | Best quality-to-speed ratio for multilingual |

Use the `.en` models for English-only channels. For long Twitch VODs, set an OpenAI or Groq key and a "use API for videos longer than N minutes" threshold in **Settings**.

## 4. TLS and domain via CloudPanel

1. In CloudPanel, go to **Sites → Add Site → Create a Reverse Proxy**. Use domain `clips.example.com` and reverse proxy URL `http://127.0.0.1:8088`.
2. Issue a Let's Encrypt certificate for the site.
3. Edit the site's **Vhost** and add the following inside the `location /` block so that live progress (SSE) streams and long uploads work:

```nginx
proxy_buffering off;
proxy_read_timeout 3600s;
proxy_send_timeout 3600s;
client_max_body_size 10m;
```

Alternatively, use a **Cloudflare Tunnel** (`cloudflared`) pointing at `http://127.0.0.1:8088`. Then you don't need to open any inbound ports. If you proxy through Cloudflare, keep the SSE endpoint uncached. API responses are already marked `no-cache`.

### Oracle networking

The stack binds only to `127.0.0.1:8088`. With CloudPanel you only need **80/443** open:

- **VCN → Security List → Ingress rules:** TCP 80 and 443 from `0.0.0.0/0`.
- **Host firewall:** Oracle's Ubuntu images ship restrictive iptables rules.

```bash
sudo iptables -I INPUT 6 -m state --state NEW -p tcp --dport 80 -j ACCEPT
sudo iptables -I INPUT 6 -m state --state NEW -p tcp --dport 443 -j ACCEPT
sudo netfilter-persistent save
```

## 5. YouTube from a datacenter IP (important)

YouTube often blocks Oracle, AWS and other cloud IPs with *"Sign in to confirm you're not a bot"* or HTTP 403 on downloads. The stack includes these mitigations, in order of effectiveness:

1. **PO tokens.** The `bgutil` sidecar is enabled by default (`YTDLP_POT_URL`).
2. **Cookies.** Go to **Settings → Upload cookies.txt**. Export it from a logged-in browser (for example with the "Get cookies.txt LOCALLY" extension) **using a throwaway Google account**, because the account can be rate-limited. Cookies expire, so re-upload them if errors come back.
3. **Proxy.** Set `YTDLP_PROXY` to a residential or ISP proxy if 1 and 2 aren't enough.
4. **Keep yt-dlp fresh.** The worker updates yt-dlp nightly at 04:43 UTC. To update now:

```bash
docker compose exec worker /opt/venv/bin/pip install -U "yt-dlp[default]"
docker compose restart worker
```

Twitch VODs and clips work without any of this.

## 6. Postiz on the same host

Run Postiz with its own compose file (see the Postiz docs) on another subdomain, e.g. `postiz.example.com`. Then:

1. In Postiz, open **Settings → Public API** and copy the API key.
2. In ClipForge, open **Settings → Postiz**. Set the URL to `https://postiz.example.com/api/public/v1` and paste the key.
3. Connect your channels (YouTube, TikTok, Instagram, …) **inside Postiz**. They then appear in the clip panel's *Publish via Postiz* box.

If Postiz runs on the same Docker host, you can join both stacks to a shared network and use the internal URL instead. The public HTTPS URL is fine too.

## 7. Operations

```bash
docker compose logs -f worker              # job logs (download / transcribe / render)
docker compose pull && docker compose up -d --build   # update images and rebuild after git pull
docker compose exec postgres pg_dump -U clipforge clipforge | gzip > backup-$(date +%F).sql.gz
docker system df                           # disk usage; media lives in the "clipforge_media" volume
```

- **Retention:** editor proxies and audio are deleted after `MEDIA_RETENTION_DAYS` (daily at 03:17 UTC). Rendered clips are kept until you delete the clip or the video.
- **CPU:** `RENDER_CONCURRENCY` and `TRANSCRIBE_CONCURRENCY` both default to 1. Ingest, transcription and rendering run in separate queues, so a long transcription never blocks renders. On 4 OCPU, keep both at 1.
- **Storage:** the boot volume on the free tier is ~47 GB by default. Attach a block volume and mount it at `/var/lib/docker` if you process lots of long VODs.
