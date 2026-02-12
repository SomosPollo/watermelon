#!/bin/sh
set -e

REPO="saeta-eth/watermelon"
INSTALL_DIR="/usr/local/bin"

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  arm64|aarch64) ARCH="arm64" ;;
  x86_64)        ARCH="amd64" ;;
  *)
    echo "Error: unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

# Detect OS (macOS only)
OS=$(uname -s)
if [ "$OS" != "Darwin" ]; then
  echo "Error: watermelon only supports macOS (detected: $OS)" >&2
  exit 1
fi

BINARY="watermelon-darwin-${ARCH}"

echo "Downloading watermelon for darwin/${ARCH}..."

# Get latest release download URL
DOWNLOAD_URL=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep "browser_download_url.*${BINARY}\"" \
  | cut -d '"' -f 4)

if [ -z "$DOWNLOAD_URL" ]; then
  echo "Error: could not find release binary for ${BINARY}" >&2
  exit 1
fi

# Download and install
TMP=$(mktemp)
curl -fsSL -o "$TMP" "$DOWNLOAD_URL"
chmod +x "$TMP"

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP" "${INSTALL_DIR}/watermelon"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mv "$TMP" "${INSTALL_DIR}/watermelon"
fi

echo "watermelon installed to ${INSTALL_DIR}/watermelon"
watermelon --version 2>/dev/null || true
