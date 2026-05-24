# syntax=docker/dockerfile:1.6
FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=1
RUN go build -o /out/samqna .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg ca-certificates curl libgomp1 build-essential git \
    && rm -rf /var/lib/apt/lists/*

# Build whisper.cpp from source (more reliable than prebuilt binaries across platforms)
RUN git clone --depth 1 --branch v1.7.4 https://github.com/ggerganov/whisper.cpp /tmp/whisper \
    && cd /tmp/whisper && make -j$(nproc) \
    && cp /tmp/whisper/main /usr/local/bin/whisper-cli \
    && rm -rf /tmp/whisper

# Download the small.en model (~466 MB) — baked into image so containers start ready
RUN mkdir -p /models && curl -L -o /models/ggml-small.en.bin \
    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.en.bin

COPY --from=builder /out/samqna /usr/local/bin/samqna
COPY view/ /app/view/
COPY static/ /app/static/
WORKDIR /app

ENV PORT=9000 \
    DATABASE_PATH=/data/samqna.db \
    MEDIA_PATH=/data/media \
    EXPORT_PATH=/data/exports \
    WHISPER_BIN=/usr/local/bin/whisper-cli \
    WHISPER_MODEL_PATH=/models/ggml-small.en.bin \
    FFMPEG_BIN=/usr/bin/ffmpeg

VOLUME ["/data"]
EXPOSE 9000
HEALTHCHECK --interval=30s --timeout=3s CMD curl -fsS http://localhost:9000/healthz || exit 1
ENTRYPOINT ["samqna"]
