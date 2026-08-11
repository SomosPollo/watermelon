#!/bin/sh
set -e

REPO="SomosPollo/watermelon"
INSTALL_DIR=${WATERMELON_INSTALL_DIR:-/usr/local/bin}

case "$INSTALL_DIR" in
  /*) ;;
  *)
    echo "Error: WATERMELON_INSTALL_DIR must be an absolute path" >&2
    exit 1
    ;;
esac

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

TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/watermelon-install.XXXXXX")
case "$TMP_ROOT" in
  "${TMPDIR:-/tmp}"/watermelon-install.*) ;;
  *)
    echo "Error: refusing unsafe temporary path: $TMP_ROOT" >&2
    exit 1
    ;;
esac
cleanup() {
  rm -rf -- "$TMP_ROOT"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM

RELEASE_JSON="$TMP_ROOT/release.json"
curl -fsSL -o "$RELEASE_JSON" "https://api.github.com/repos/${REPO}/releases/latest"

DOWNLOAD_URL=$(grep "browser_download_url.*${BINARY}\"" "$RELEASE_JSON" \
  | cut -d '"' -f 4 \
  | head -n 1 || true)

if [ -z "$DOWNLOAD_URL" ]; then
  echo "Error: could not find release binary for ${BINARY}" >&2
  exit 1
fi

SIDECAR_URL=$(grep "browser_download_url.*${SIDECAR}\"" "$RELEASE_JSON" \
  | cut -d '"' -f 4 \
  | head -n 1 || true)

CLI_TMP="$TMP_ROOT/watermelon"
curl -fsSL -o "$CLI_TMP" "$DOWNLOAD_URL"

SIDECAR_TMP=
if [ -n "$SIDECAR_URL" ]; then
  echo "Downloading watermelon network interceptor for linux/${ARCH}..."
  SIDECAR_TMP="$TMP_ROOT/$SIDECAR"
  curl -fsSL -o "$SIDECAR_TMP" "$SIDECAR_URL"
else
  echo "Warning: release sidecar ${SIDECAR} not found; ask-mode will require WATERMELON_NFQD_BINARY" >&2
fi

if [ ! -d "$INSTALL_DIR" ]; then
  if mkdir -p "$INSTALL_DIR" 2>/dev/null; then
    :
  elif command -v sudo >/dev/null 2>&1; then
    echo "Creating ${INSTALL_DIR} (requires sudo)..."
    sudo mkdir -p "$INSTALL_DIR"
  else
    echo "Error: ${INSTALL_DIR} does not exist and cannot be created; set WATERMELON_INSTALL_DIR to a writable absolute path" >&2
    exit 1
  fi
fi

USE_SUDO=false
if [ ! -w "$INSTALL_DIR" ]; then
  if ! command -v sudo >/dev/null 2>&1; then
    echo "Error: ${INSTALL_DIR} is not writable and sudo is unavailable; set WATERMELON_INSTALL_DIR to a writable absolute path" >&2
    exit 1
  fi
  USE_SUDO=true
fi

install_executable() {
  source_path=$1
  destination_path=$2
  if [ "$USE_SUDO" = true ]; then
    sudo install -m 0755 "$source_path" "$destination_path"
  else
    install -m 0755 "$source_path" "$destination_path"
  fi
}

if [ -n "$SIDECAR_TMP" ]; then
  install_executable "$SIDECAR_TMP" "${INSTALL_DIR}/${SIDECAR}"
fi
install_executable "$CLI_TMP" "${INSTALL_DIR}/watermelon"

echo "watermelon installed to ${INSTALL_DIR}/watermelon"
"${INSTALL_DIR}/watermelon" --version
