#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

if ! command -v pandoc &>/dev/null; then
    echo "Error: pandoc is required. Install via: brew install pandoc"
    exit 1
fi

echo "Building whitepaper PDF..."
pandoc fleet-llm-d-whitepaper.md \
    -o fleet-llm-d-whitepaper.pdf \
    --pdf-engine=xelatex \
    -V geometry:margin=1in \
    -V fontsize=11pt \
    -V mainfont="Helvetica" \
    --toc \
    --number-sections \
    --highlight-style=tango

echo "Built: fleet-llm-d-whitepaper.pdf"
