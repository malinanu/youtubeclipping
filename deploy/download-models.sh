#!/usr/bin/env bash
# Downloads whisper.cpp GGML models (mounted read-only at /models).
# Usage: [MODELS_DIR=/opt/clipforge/models] ./download-models.sh [model ...]   default: small
#   Target defaults to ./models next to this script (standalone compose).
#   Common: tiny, base, small, medium, large-v3-turbo  (append .en for English-only, e.g. small.en)
set -euo pipefail
dir="${MODELS_DIR:-$(cd "$(dirname "$0")" && pwd)/models}"
mkdir -p "$dir"
models=("$@")
[ ${#models[@]} -eq 0 ] && models=(small)
for m in "${models[@]}"; do
  if [[ ! "$m" =~ ^[a-z0-9.\-]+$ ]]; then echo "invalid model name: $m" >&2; exit 1; fi
  out="$dir/ggml-${m}.bin"
  if [ -s "$out" ]; then echo "✓ $out exists"; continue; fi
  echo "↓ $m"
  curl -fL --retry 3 -o "$out.part" "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-${m}.bin"
  mv "$out.part" "$out"
done
chmod a+r "$dir"/*.bin
ls -lh "$dir"
