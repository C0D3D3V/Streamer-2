# ── Stage 1: Build the React frontend ────────────────────────────────────────
FROM node:20-alpine AS frontend-builder

WORKDIR /app/frontend

# Install dependencies first (cached layer if package.json unchanged).
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci

# Copy source and build.
COPY frontend/ ./
RUN npm run build
# Output: /app/frontend/dist

# ── Stage 2: Build the Go binary ─────────────────────────────────────────────
FROM golang:1.26-alpine AS go-builder

# git is needed for `go mod download` with some VCS modules.
RUN apk add --no-cache git

WORKDIR /app

# Cache Go module downloads as a separate layer.
COPY go.mod go.sum ./
RUN go mod download

# Copy the Go source first, then overwrite the placeholder with the real
# frontend build. Order matters: COPY . . would overwrite the frontend if
# it came after, so the frontend copy must be last.
COPY . .
COPY --from=frontend-builder /app/frontend/dist ./internal/webui/dist

# CGO_ENABLED=0 produces a fully static binary with no libc dependency.
# This is possible because glebarez/sqlite is a pure-Go SQLite port (modernc.org/sqlite),
# unlike mattn/go-sqlite3 which embeds C code and requires a C compiler.
# A static binary also means the arm64 cross-build runs via the native Go toolchain
# rather than QEMU emulation, keeping build times fast.
RUN CGO_ENABLED=0 GOOS=linux go build -o /streamer ./cmd/server

# ── Stage 3: Runtime image ────────────────────────────────────────────────────
# Default: Alpine with software ffmpeg (works on any CPU, no GPU required).
# For Intel QSV or VA-API hardware acceleration, use the -qsv build target:
#   docker build --target runtime-qsv -t streamer:qsv .
FROM alpine:3.20 AS runtime-base

# ffmpeg    – HLS ingest pipeline and archive MP4 finalization
# su-exec   – lightweight privilege-drop helper used by the entrypoint script
#             to run the app as the user-specified PUID/PGID instead of root
RUN apk add --no-cache ffmpeg ca-certificates su-exec

# ── Stage 3a: QSV / VA-API runtime (Intel GPU) ───────────────────────────────
# Use this stage if you have an Intel GPU and want hardware-accelerated
# archive finalization (set hwaccel: qsv or hwaccel: vaapi in config.yaml).
#
# Requires the host to expose the GPU render node:
#   docker run --device /dev/dri:/dev/dri ...
# Or in docker-compose:
#   devices:
#     - /dev/dri:/dev/dri
FROM alpine:3.20 AS runtime-qsv

# intel-media-driver provides the iHD VA-API driver for Gen8+ Intel GPUs.
# libva-intel-driver provides the i965 driver for older Gen4–Gen7 GPUs.
# mesa-va-gallium covers AMD and newer Intel via open-source Mesa drivers.
# libva-utils provides vainfo for verifying hardware acceleration is working.
RUN apk add --no-cache \
      ffmpeg \
      ca-certificates \
      su-exec \
      intel-media-driver \
      libva-intel-driver \
      libva-utils \
      mesa-va-gallium

# ── Pick the default runtime stage ───────────────────────────────────────────
# Override with: docker build --target runtime-qsv ...
FROM runtime-base

WORKDIR /app

COPY --from=go-builder /streamer ./streamer
COPY entrypoint.sh ./entrypoint.sh
RUN chmod +x ./entrypoint.sh

# /data holds the SQLite database, HLS segments, and MP4 archives.
# Ownership is set at runtime by the entrypoint script to match PUID/PGID.
RUN mkdir -p /data

EXPOSE 8080

ENV CONFIG_PATH=/data/config.yaml
ENV DATA_DIR=/data
# Default UID/GID/UMASK – override in docker-compose or with -e flags.
ENV PUID=1000
ENV PGID=1000
ENV UMASK=022

# The entrypoint runs as root so it can chown /data and call su-exec.
# It then drops to PUID:PGID before exec-ing the application.
ENTRYPOINT ["./entrypoint.sh"]
