# Streamer

A self-hosted live streaming application. Stream your camera and microphone from a mobile device to multiple viewers. Recordings are automatically saved as MP4 files.

## Features

- **Live streaming** — stream camera + microphone from any browser that supports `getUserMedia`
- **Multiple viewers** — a DASH/HLS CMAF pipeline lets any number of viewers watch simultaneously; DASH.js is used on most browsers, native HLS on Safari
- **Shared links** — a public watch link is auto-created for every stream; additional links can be added with optional password and expiry
- **Scheduled streams** — plan a stream in advance; viewers see the scheduled start time and the page auto-updates when streaming begins
- **Resolution tiers** — streamer chooses HD (1280), FHD (1920), or QHD (2560) long-side; the short side is derived from the camera's native aspect ratio so no 16:9 cropping is forced; an optional 16:9 toggle is shown when the camera is not already 16:9
- **Automatic recording** — every stream is recorded and finalized to MP4 after it ends
- **Archive** — browse, watch, share, and delete past recordings
- **OIDC login** — authentication via [Authelia](https://www.authelia.com/) (or any OIDC provider)
- **First-run setup wizard** — configure OIDC on first launch via a web UI; settings saved to `config.yaml`

---

## Quick Start (Docker)

```bash
# 1. Copy and edit the compose file
cp docker-compose.yml my-compose.yml
# Edit my-compose.yml: set the external URL, optionally pre-populate config.yaml

# 2. Start the container
docker compose -f my-compose.yml up -d

# 3. Open the setup wizard
open http://localhost:8080
```

On first launch you will be redirected to the setup wizard at `/setup` to enter your OIDC client credentials.

---

## Setting Up the Authelia OIDC Client

Streamer uses OpenID Connect (OIDC) with the **Authorization Code + PKCE** flow.

### 1. Register the client in Authelia

Add the following to your Authelia `configuration.yml` under `identity_providers.oidc.clients`:

```yaml
identity_providers:
  oidc:
    ## (your existing OIDC config…)
    clients:
      - client_id: streamer
        client_name: Streamer
        # Generate a strong secret: openssl rand -hex 32
        client_secret: "$pbkdf2-sha512$310000$<hash>"  # bcrypt/pbkdf2 hash of your secret
        public: false
        authorization_policy: one_factor   # or two_factor
        require_pkce: true
        pkce_challenge_method: S256
        redirect_uris:
          - https://stream.example.com/auth/callback
        scopes:
          - openid
          - email
          - profile
        response_types:
          - code
        grant_types:
          - authorization_code
        access_token_signed_response_alg: none
        userinfo_signed_response_alg: none
        token_endpoint_auth_method: client_secret_basic
```

> **Tip:** Generate the client secret with `openssl rand -hex 32`. Hash it with Authelia's `authelia crypto hash generate pbkdf2 --variant sha512`.

### 2. Reload Authelia

```bash
docker compose exec authelia authelia config validate
docker compose restart authelia
```

### 3. Complete the setup wizard

Open `https://stream.example.com` and fill in:

| Field | Value |
|---|---|
| Authelia Issuer URL | `https://auth.example.com` |
| OIDC Client ID | `streamer` |
| OIDC Client Secret | the **plain** secret (not the hash) |
| External URL | `https://stream.example.com` |

The wizard saves these to `config.yaml` and redirects you to the login page.

---

## Configuration Reference (`config.yaml`)

The file is created by the setup wizard. You can edit it manually and restart the container.

```yaml
server:
  host: "0.0.0.0"
  port: "8080"
  # Public-facing URL used to build OAuth2 redirect URIs.
  external_url: "https://stream.example.com"

database:
  # "sqlite" or "postgres"
  driver: "sqlite"
  # SQLite: path to the .db file
  # Postgres: "host=db user=streamer password=secret dbname=streamer sslmode=disable"
  dsn: "data/streamer.db"

oidc:
  issuer_url: "https://auth.example.com"
  client_id: "streamer"
  client_secret: "your-plain-secret"
  redirect_url: "https://stream.example.com/auth/callback"
  scopes:
    - openid
    - email
    - profile

app:
  # Root directory for HLS segments and MP4 archives.
  data_dir: "data"
  # Keep raw .ts segments after MP4 finalization? (default: false)
  keep_segments_after_finalization: false
  # Safety limit in minutes. 0 = unlimited.
  max_stream_duration_minutes: 0
  # Hardware acceleration backend for ffmpeg (default: none = software).
  # See "Hardware Acceleration" section below for setup instructions.
  hwaccel: "none"
```

---

## Hardware Acceleration (QSV / NVENC / VA-API)

Hardware acceleration affects the **archive finalizer** only. The live ingest pipeline always stream-copies (the browser already encoded the video), so hardware acceleration does not change live latency.

When `hwaccel` is set, the finalizer re-encodes the archive to H.264 using the GPU instead of stream-copying. This offloads CPU work and can be significantly faster on long recordings.

| Value | Hardware | ffmpeg encoder |
|---|---|---|
| `none` (default) | Software, any CPU | stream copy (lossless, fast) |
| `qsv` | Intel Quick Sync Video (Gen6+) | `h264_qsv` |
| `nvenc` | NVIDIA GPU | `h264_nvenc` |
| `vaapi` | Intel / AMD via VA-API | `h264_vaapi` |

### Intel QSV / VA-API setup

**1. Build the QSV Docker image:**

```bash
docker build --target runtime-qsv -t streamer:qsv .
```

**2. Expose the GPU device to the container** in `docker-compose.yml`:

```yaml
services:
  streamer:
    image: streamer:qsv
    devices:
      - /dev/dri:/dev/dri   # Intel GPU render node
```

**3. Set the backend in `config.yaml`:**

```yaml
app:
  hwaccel: "qsv"   # or "vaapi" for older Intel / AMD GPUs
```

**Verify QSV is available inside the container:**

```bash
docker exec -it streamer vainfo
# Should list VAEntrypointEncSlice for H264
```

### NVIDIA NVENC setup

**1. Use the standard image** (Alpine's ffmpeg includes nvenc support).

**2. Add the NVIDIA runtime** to `docker-compose.yml`:

```yaml
services:
  streamer:
    runtime: nvidia
    environment:
      - NVIDIA_VISIBLE_DEVICES=all
      - NVIDIA_DRIVER_CAPABILITIES=video,compute,utility
```

**3. Set the backend in `config.yaml`:**

```yaml
app:
  hwaccel: "nvenc"
```

### Switching to PostgreSQL

Change the `database` section and restart:

```yaml
database:
  driver: postgres
  dsn: "host=db user=streamer password=secret dbname=streamer sslmode=disable"
```

GORM handles all schema migrations automatically on startup.

---

## Architecture Overview

```
Mobile Browser                    Go Server (Gin)              Viewers
──────────────                    ───────────────              ───────
getUserMedia()                    ┌──────────────────────┐
   │                              │  /api/streams        │
   │ POST /api/streams/{id}/start │  /api/streams/{id}/  │
   ├─────────────────────────────▶│    start             │
   │                              │    ingest  ──▶ ffmpeg│──▶ DASH fMP4 segments (.m4s)
   │ POST /api/streams/{id}/ingest│    stop              │     + DASH manifest (.mpd)
   ├─────────────────────────────▶│                      │     + HLS playlist (.m3u8)
   │ (WebM chunks every ~2s)      │  /live/{id}/         │         │
   │                              │    manifest.mpd      │◀────────┘
   │                              │    master.m3u8       │──────────────────────▶
   │                              │    *.m4s             │     DASH.js / native HLS (Safari)
   │                              │  /ws/stream/{id}     │──▶ WebSocket events
   │                              │                      │     stream.started
   │                              │  archive finalizer   │     stream.ended
   │                              │  (ffmpeg concat→MP4) │
   │                              └──────────────────────┘
```

**Streaming pipeline:**
1. The streamer's browser encodes video with `MediaRecorder` (WebM/H.264+Opus)
2. Encoded chunks are POSTed to the Go server every ~2 seconds
3. A long-running `ffmpeg` process receives the WebM bytes via stdin and outputs CMAF fMP4 segments
4. ffmpeg's DASH muxer writes `.m4s` segment files and keeps the `manifest.mpd` and `master.m3u8` updated
5. Viewers fetch the manifest and segments via DASH.js; Safari uses native HLS
6. After the stream ends, another `ffmpeg` run concatenates the fMP4 segments into a single `.mp4` (stream copy, moov atom moved to front for progressive playback)

---

## Development Setup

### Prerequisites
- Go 1.21+
- Node.js 20+
- ffmpeg (in PATH)

### Run the backend

```bash
go run ./cmd/server
# Server starts on :8080
```

### Run the frontend (hot reload)

```bash
cd frontend
npm install
npm run dev
# Vite dev server on :5173, proxies /api, /auth, /live, /ws to :8080
```

### Build for production

```bash
cd frontend && npm run build
# Output goes to frontend/dist
cp -r frontend/dist internal/webui/dist
go build -o streamer ./cmd/server
./streamer
```

---

## Docker Build

The GitHub Actions workflow (`.github/workflows/docker.yml`) builds and pushes to GitHub Container Registry on every push to `main` and on version tags (`v*`).

To build locally:

```bash
docker build -t streamer:local .
```

The multi-stage build:
1. **Stage 1** — Node 20: `npm run build` → `frontend/dist`
2. **Stage 2** — Go 1.23: compile binary with frontend embedded via `go:embed`
3. **Stage 3** — Alpine + ffmpeg: minimal runtime image (~50 MB)

---

## Security Notes

- All cookies are `HttpOnly; SameSite=Lax`
- PKCE (S256) is used for the OIDC flow — no implicit grant
- HLS segment URLs require either a valid session cookie or a viewer JWT (issued after password check)
- File paths for HLS serving are validated against the configured `data_dir` to prevent path traversal
- Password-protected watch links use bcrypt (cost 10)
- Viewer JWTs expire after 1 hour
