#!/usr/bin/env bash
# Tiny wrapper around cmd/vless-vet so it can be called as a single
# script with positional arguments. The Go tool itself takes flags;
# this just maps INPUT [OUTPUT] to -in / -out for muscle-memory.
#
# Usage:
#   scripts/vless-vet.sh INPUT [OUTPUT]
#   scripts/vless-vet.sh path/to/list.txt
#   scripts/vless-vet.sh path/to/list.txt path/to/alive.txt
#
# All extra arguments are forwarded to the Go tool, so you can tweak
# probes:
#   scripts/vless-vet.sh in.txt out.txt -workers 128 -tcp-timeout 2s
#
# Skip the TLS handshake check (faster but less informative — only
# tells you the port is open, not that Reality is configured):
#   scripts/vless-vet.sh in.txt -skip-tls

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [ $# -lt 1 ]; then
    echo "usage: $0 INPUT [OUTPUT] [-- extra flags]" >&2
    exit 2
fi

INPUT="$1"
shift

OUTPUT=""
if [ $# -gt 0 ] && [[ "$1" != -* ]]; then
    OUTPUT="$1"
    shift
fi

cd "$ROOT"
if [ -n "$OUTPUT" ]; then
    exec go run ./cmd/vless-vet -in "$INPUT" -out "$OUTPUT" "$@"
else
    exec go run ./cmd/vless-vet -in "$INPUT" "$@"
fi
