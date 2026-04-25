#!/usr/bin/env bash
#
# Cross-compile a static, musl-friendly sing-box binary for OpenWrt aarch64
# (GL.iNet MT3000 / Beryl, target mediatek/mt7981).
#
# The official SagerNet release tarballs link against glibc (libcronet bundle)
# and segfault on OpenWrt's musl libc. We build with CGO_ENABLED=0 to get a
# pure-Go static binary that runs anywhere.
#
# Requirements: Go 1.22+ on the build host.
#
# Output: build/sing-box-build/sing-box-static

set -euo pipefail

VERSION="${SING_BOX_VERSION:-v1.13.11}"
TARGET_OS="${TARGET_OS:-linux}"
TARGET_ARCH="${TARGET_ARCH:-arm64}"
TAGS="${SING_BOX_TAGS:-with_quic,with_grpc,with_dhcp,with_wireguard,with_utls,with_clash_api,with_gvisor}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR="$ROOT/build/sing-box-build"
OUT="$BUILD_DIR/sing-box-static"

mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"

if [ ! -d sing-box ]; then
    echo ">>> cloning sing-box $VERSION"
    git clone --depth 1 --branch "$VERSION" https://github.com/SagerNet/sing-box.git
else
    echo ">>> sing-box checkout already present, skipping clone"
fi

cd sing-box

echo ">>> building $TARGET_OS/$TARGET_ARCH (CGO disabled, tags: $TAGS)"
CGO_ENABLED=0 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" \
    go build -trimpath \
        -ldflags "-s -w -X github.com/sagernet/sing-box/constant.Version=${VERSION#v}" \
        -tags "$TAGS" \
        -o "$OUT" \
        ./cmd/sing-box

echo ">>> done: $OUT"
ls -la "$OUT"
file "$OUT" 2>/dev/null || true
