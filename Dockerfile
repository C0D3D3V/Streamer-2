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
FROM alpine:3.20 AS runtime-base

# ffmpeg    – HLS ingest pipeline and archive MP4 finalization
# su-exec   – lightweight privilege-drop helper used by the entrypoint script
#             to run the app as the user-specified PUID/PGID instead of root
RUN apk add --no-cache ffmpeg ca-certificates su-exec

# ── Stage 3a: QSV / VA-API runtime (Intel GPU, Debian) ───────────────────────
# Debian bookworm-slim with Intel GPU drivers for VA-API and QSV encoding.
#
# intel-media-va-driver – iHD VA-API driver for Gen8+ Intel GPUs (non-free)
# libmfx-gen1.2         – oneVPL GPU plugin for Gen12+ QSV; trixie's ffmpeg 7.x
#                         uses --enable-libvpl so this is the correct runtime lib
# vainfo                – VA-API info CLI (binary package from libva-utils source)
# ffmpeg                – compiled with --enable-vaapi and --enable-libmfx
# gosu                  – privilege-drop helper (same interface as su-exec)
#
# Requires the host to expose the GPU render node:
#   devices:
#     - /dev/dri:/dev/dri
FROM debian:trixie-slim AS runtime-qsv

# Replace the DEB822 sources file with a classic sources.list that includes
# non-free so intel-media-va-driver is reachable. libmfx-gen1.2 is in main.
RUN rm /etc/apt/sources.list.d/debian.sources \
    && echo "deb http://deb.debian.org/debian trixie main contrib non-free non-free-firmware" > /etc/apt/sources.list \
    && echo "deb http://deb.debian.org/debian trixie-updates main contrib non-free non-free-firmware" >> /etc/apt/sources.list \
    && echo "deb http://deb.debian.org/debian-security trixie-security main contrib non-free non-free-firmware" >> /etc/apt/sources.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
         ffmpeg \
         ca-certificates \
         gosu \
         intel-media-va-driver \
         libmfx-gen1.2 \
         vainfo \
    && rm -rf /var/lib/apt/lists/*

# ── Stage 3b: QSV / VA-API runtime (Intel GPU, Ubuntu variant) ───────────────
# Alternative to runtime-qsv using Ubuntu 24.04. onevpl-intel-gpu is available
# in Ubuntu's universe repository without needing Intel's external apt repo.
FROM ubuntu:24.04 AS runtime-ubuntu-qsv

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends software-properties-common \
    && add-apt-repository universe \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
         ffmpeg \
         ca-certificates \
         gosu \
         intel-media-va-driver \
         libmfx-gen1.2 \
         vainfo \
    && rm -rf /var/lib/apt/lists/*

# ── Stage 4: Assemble the final images ───────────────────────────────────────
# Named targets differ only in their base OS / driver set.

FROM runtime-base AS app
WORKDIR /app
COPY --from=go-builder /streamer ./streamer
COPY entrypoint.sh ./entrypoint.sh
RUN chmod +x ./entrypoint.sh
RUN mkdir -p /data
EXPOSE 8080
ENV CONFIG_PATH=/data/config.yaml
ENV DATA_DIR=/data
ENV PUID=1000
ENV PGID=1000
ENV UMASK=022
ENTRYPOINT ["./entrypoint.sh"]

FROM runtime-qsv AS app-qsv
WORKDIR /app
COPY --from=go-builder /streamer ./streamer
COPY entrypoint.sh ./entrypoint.sh
RUN chmod +x ./entrypoint.sh
RUN mkdir -p /data
EXPOSE 8080
ENV CONFIG_PATH=/data/config.yaml
ENV DATA_DIR=/data
ENV PUID=1000
ENV PGID=1000
ENV UMASK=022
ENTRYPOINT ["./entrypoint.sh"]

FROM runtime-ubuntu-qsv AS app-ubuntu-qsv
WORKDIR /app
COPY --from=go-builder /streamer ./streamer
COPY entrypoint.sh ./entrypoint.sh
RUN chmod +x ./entrypoint.sh
RUN mkdir -p /data
EXPOSE 8080
ENV CONFIG_PATH=/data/config.yaml
ENV DATA_DIR=/data
ENV PUID=1000
ENV PGID=1000
ENV UMASK=022
ENTRYPOINT ["./entrypoint.sh"]
