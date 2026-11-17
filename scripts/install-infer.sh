#!/usr/bin/env bash
# Infer v1.3.0 の公式バイナリを .tools/infer に展開する。
set -euo pipefail

VERSION=v1.3.0
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/.tools/infer"

case "$(uname -s)-$(uname -m)" in
  Darwin-arm64) PLATFORM=osx-arm64 ;;
  Linux-x86_64) PLATFORM=linux-x86_64 ;;
  *) echo "unsupported platform: $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

if [ -x "$DEST/bin/infer" ] && "$DEST/bin/infer" --version 2>/dev/null | grep -q "$VERSION"; then
  echo "infer $VERSION is already installed in $DEST"
  exit 0
fi

NAME="infer-$PLATFORM-$VERSION"
URL="https://github.com/facebook/infer/releases/download/$VERSION/$NAME.tar.xz"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "downloading $URL" >&2
curl -fL --retry 3 -o "$TMP/infer.tar.xz" "$URL"
mkdir -p "$TMP/extract"
tar -xJf "$TMP/infer.tar.xz" -C "$TMP/extract"

rm -rf "$DEST"
mkdir -p "$(dirname "$DEST")"
mv "$TMP/extract/$NAME" "$DEST"
echo "installed infer to $DEST" >&2
