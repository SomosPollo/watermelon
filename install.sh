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

# Detect OS
OS=$(uname -s)
case "$OS" in
  Darwin) OS_NAME="darwin" ;;
  Linux)  OS_NAME="linux" ;;
  *)
    echo "Error: unsupported OS: $OS (watermelon supports macOS and Linux)" >&2
    exit 1
    ;;
esac

BINARY="watermelon-${OS_NAME}-${ARCH}"
SIDECAR="watermelon-nfqd-linux-${ARCH}"

echo "Downloading watermelon for ${OS_NAME}/${ARCH}..."

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

SIDECAR_URL=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep "browser_download_url.*${SIDECAR}\"" \
  | cut -d '"' -f 4 || true)

if [ -n "$SIDECAR_URL" ]; then
  echo "Downloading watermelon network interceptor for linux/${ARCH}..."
  SIDECAR_TMP=$(mktemp)
  curl -fsSL -o "$SIDECAR_TMP" "$SIDECAR_URL"
  chmod +x "$SIDECAR_TMP"

  if [ -w "$INSTALL_DIR" ]; then
    mv "$SIDECAR_TMP" "${INSTALL_DIR}/${SIDECAR}"
  else
    echo "Installing network interceptor to ${INSTALL_DIR} (requires sudo)..."
    sudo mv "$SIDECAR_TMP" "${INSTALL_DIR}/${SIDECAR}"
  fi
else
  echo "Warning: release sidecar ${SIDECAR} not found; ask-mode will require WATERMELON_NFQD_BINARY" >&2
fi

echo "watermelon installed to ${INSTALL_DIR}/watermelon"
watermelon --version 2>/dev/null || true
