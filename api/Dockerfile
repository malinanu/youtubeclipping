# syntax=docker/dockerfile:1.7
# Backend image: Go API + worker, yt-dlp, ffmpeg (libass), whisper.cpp, deno, caption fonts.
# Built natively on the target host (Oracle Ampere A1 = linux/arm64) so whisper.cpp
# is compiled with the CPU's best SIMD flags (GGML_NATIVE=ON).

ARG WHISPER_VERSION=v1.8.2
ARG DENO_VERSION=2.5.0
# Whisper model(s) baked into the image (space-separated), e.g. "small" or "small medium".
ARG WHISPER_MODELS="small"

FROM denoland/deno:bin-${DENO_VERSION} AS deno

FROM ubuntu:24.04 AS whisper
ARG WHISPER_VERSION
ARG WHISPER_NATIVE=ON
# Portable CI builds: WHISPER_NATIVE=OFF + an explicit target, e.g. armv8.2-a+dotprod+fp16 (Ampere Altra).
ARG WHISPER_ARM_ARCH=""
RUN apt-get update && apt-get install -y --no-install-recommends build-essential cmake git ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch ${WHISPER_VERSION} https://github.com/ggml-org/whisper.cpp /src
WORKDIR /src
RUN cmake -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DWHISPER_BUILD_TESTS=OFF \
      -DGGML_NATIVE=${WHISPER_NATIVE} -DGGML_OPENMP=ON \
      ${WHISPER_ARM_ARCH:+-DGGML_CPU_ARM_ARCH=${WHISPER_ARM_ARCH}} \
    && cmake --build build -j"$(nproc)" --target whisper-cli

# Download models in their own stage so the layer is cached across code changes.
FROM ubuntu:24.04 AS models
ARG WHISPER_MODELS
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /opt/whisper-models && for m in ${WHISPER_MODELS}; do \
      curl -fL --retry 5 --retry-delay 5 -o /opt/whisper-models/ggml-$m.bin \
        "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-$m.bin" \
      && test "$(stat -c %s /opt/whisper-models/ggml-$m.bin)" -gt 1000000 || exit 1; \
    done && chmod a+r /opt/whisper-models/*.bin && ls -lh /opt/whisper-models

FROM golang:1.26-bookworm AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

FROM ubuntu:24.04
LABEL org.opencontainers.image.source="https://github.com/malinanu/youtubeclipping" \
      org.opencontainers.image.description="ClipForge API + worker (yt-dlp, ffmpeg, whisper.cpp)"
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl tini ffmpeg libgomp1 python3 python3-venv fontconfig \
      fonts-montserrat fonts-inter fonts-bebas-neue fonts-league-spartan fonts-lato fonts-roboto fonts-noto-core \
    && rm -rf /var/lib/apt/lists/* && fc-cache -f

RUN useradd --create-home --uid 10001 app
# yt-dlp lives in a venv owned by the app user so the nightly job can self-update it.
RUN python3 -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir "yt-dlp[default]" bgutil-ytdlp-pot-provider \
    && chown -R app:app /opt/venv
ENV PATH=/opt/venv/bin:$PATH

COPY --from=deno /deno /usr/local/bin/deno
COPY --from=whisper /src/build/bin/whisper-cli /usr/local/bin/whisper-cli
COPY --from=gobuild /out/ /usr/local/bin/
COPY --from=models /opt/whisper-models /opt/whisper-models

RUN mkdir -p /data /models && chown app:app /data /models
USER app
ENV DATA_DIR=/data \
    WHISPER_MODELS_DIR=/models \
    WHISPER_BUNDLED_DIR=/opt/whisper-models \
    YTDLP_COOKIES_FILE=/data/cookies.txt \
    YTDLP_UPDATE_CMD="/opt/venv/bin/pip install -U --no-cache-dir yt-dlp[default] bgutil-ytdlp-pot-provider" \
    HTTP_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["tini", "--"]
CMD ["api"]
