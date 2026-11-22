#!/usr/bin/env bash
# Downloads whisper.cpp GGML models into ./models (mounted read-only at /models).
# Usage: ./download-models.sh [model ...]   default: small
#   Common: tiny, base, small, medium, large-v3-turbo  (append .en for English-only, e.g. small.en)
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p models
models=("$@")
[ ${#models[@]} -eq 0 ] && models=(small)
for m in "${models[@]}"; do
  if [[ ! "$m" =~ ^[a-z0-9.\-]+$ ]]; then echo "invalid model name: $m" >&2; exit 1; fi
  out="models/ggml-${m}.bin"
  if [ -s "$out" ]; then echo "✓ $out exists"; continue; fi
  echo "↓ $m"
  curl -fL --retry 3 -o "$out.part" "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-${m}.bin"
  mv "$out.part" "$out"
done
ls -lh models
