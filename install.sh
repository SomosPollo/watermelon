#!/bin/sh
set -e

REPO="SomosPollo/watermelon"
INSTALL_DIR=${WATERMELON_INSTALL_DIR:-/usr/local/bin}
MINIMUM_LIMA_VERSION="2.0.0"
MINIMUM_MACOS_VERSION="13"

case "$INSTALL_DIR" in
  /*) ;;
  *)
    echo "Error: WATERMELON_INSTALL_DIR must be an absolute path" >&2
    exit 1
    ;;
esac

require_command() {
  command_name=$1
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "Error: required command not found: ${command_name}" >&2
    case "${command_name}:${OS_NAME:-}" in
      curl:darwin) echo "Install curl with: brew install curl" >&2 ;;
      curl:linux) echo "Install curl with your system package manager and run this installer again" >&2 ;;
      ssh:darwin) echo "Restore the macOS OpenSSH client so ssh is available in PATH" >&2 ;;
      ssh:linux) echo "Install the OpenSSH client with your system package manager (for example, openssh-client)" >&2 ;;
      awk:*|mktemp:*|install:*) echo "Restore the standard ${command_name} system utility and run this installer again" >&2 ;;
    esac
    exit 1
  fi
}

# Detect the architecture of the host process. This selects the watermelon CLI;
# the Linux sidecar architecture is selected from Lima below so that installation
# also works when this script runs through Rosetta on Apple silicon.
HOST_ARCH=$(uname -m)
case "$HOST_ARCH" in
  arm64|aarch64) CLI_ARCH="arm64" ;;
  x86_64)        CLI_ARCH="amd64" ;;
  *)
    echo "Error: unsupported architecture: $HOST_ARCH" >&2
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

extract_macos_major_version() {
  macos_candidate=$1
  case "$macos_candidate" in
    ''|.*|*.|*..*|*.*.*.*|*[!0-9.]*) return 1 ;;
  esac

  macos_remaining=$macos_candidate
  macos_major=${macos_candidate%%.*}
  while :; do
    macos_component=${macos_remaining%%.*}
    case "$macos_component" in
      0) ;;
      0*|??????????*) return 1 ;;
    esac
    case "$macos_remaining" in
      *.*) macos_remaining=${macos_remaining#*.} ;;
      *) break ;;
    esac
  done

  printf '%s\n' "$macos_major"
}

# VZ behavior used by Watermelon requires macOS 13 or newer. Check the host
# before probing Lima or contacting GitHub so an unsupported host gets a direct
# and actionable error.
if [ "$OS_NAME" = "darwin" ]; then
  if ! SW_VERS_PATH=$(command -v sw_vers 2>/dev/null); then
    echo "Error: sw_vers was not found; watermelon requires macOS ${MINIMUM_MACOS_VERSION} or newer" >&2
    echo "Run 'sw_vers -productVersion' and upgrade this Mac to macOS ${MINIMUM_MACOS_VERSION} or newer" >&2
    exit 1
  fi
  if ! MACOS_VERSION_OUTPUT=$("$SW_VERS_PATH" -productVersion 2>&1); then
    echo "Error: failed to read the macOS product version with ${SW_VERS_PATH} -productVersion" >&2
    printf '%s\n' "$MACOS_VERSION_OUTPUT" >&2
    echo "Upgrade this Mac to macOS ${MINIMUM_MACOS_VERSION} or newer, then run the installer again" >&2
    exit 1
  fi
  if ! MACOS_MAJOR_VERSION=$(extract_macos_major_version "$MACOS_VERSION_OUTPUT"); then
    echo "Error: could not parse the macOS product version reported by ${SW_VERS_PATH}: ${MACOS_VERSION_OUTPUT}" >&2
    echo "Run 'sw_vers -productVersion' and ensure it reports macOS ${MINIMUM_MACOS_VERSION} or newer" >&2
    exit 1
  fi
  if [ "$MACOS_MAJOR_VERSION" -lt "$MINIMUM_MACOS_VERSION" ]; then
    echo "Error: macOS ${MACOS_VERSION_OUTPUT} is unsupported; watermelon requires macOS ${MINIMUM_MACOS_VERSION} or newer" >&2
    echo "Upgrade this Mac to macOS ${MINIMUM_MACOS_VERSION} or newer, then run the installer again" >&2
    exit 1
  fi
fi

for command_name in curl mktemp install ssh awk; do
  require_command "$command_name"
done

show_lima_install_help() {
  case "$OS_NAME" in
    darwin)
      echo "Install Lima with 'brew install lima', or upgrade it with 'brew upgrade lima'" >&2
      ;;
    linux)
      echo "Install or upgrade Lima and QEMU: https://lima-vm.io/docs/installation/" >&2
      ;;
  esac
}

if ! LIMACTL_PATH=$(command -v limactl 2>/dev/null); then
  echo "Error: limactl was not found in PATH; watermelon requires Lima ${MINIMUM_LIMA_VERSION} or newer" >&2
  show_lima_install_help
  exit 1
fi

extract_lima_version() {
  case "$1" in
    "limactl version "*) candidate=${1#limactl version } ;;
    *) return 1 ;;
  esac
  candidate=${candidate#v}
  case "$candidate" in
    ''|.*|*.|*..*|*.*.*.*|*[!0-9.]*) return 1 ;;
  esac

  candidate_major=${candidate%%.*}
  candidate_rest=${candidate#*.}
  [ "$candidate_rest" != "$candidate" ] || return 1
  candidate_minor=${candidate_rest%%.*}
  candidate_patch=${candidate_rest#*.}
  [ "$candidate_patch" != "$candidate_rest" ] || return 1
  case "$candidate_patch" in *.*) return 1 ;; esac
  for component in "$candidate_major" "$candidate_minor" "$candidate_patch"; do
    case "$component" in
      0) ;;
      0*) return 1 ;;
    esac
  done

  printf '%s\n' "$candidate"
}

version_at_least() {
  candidate=$1
  minimum=$2

  candidate_major=${candidate%%.*}
  candidate_rest=${candidate#*.}
  candidate_minor=${candidate_rest%%.*}
  candidate_patch=${candidate_rest#*.}
  minimum_major=${minimum%%.*}
  minimum_rest=${minimum#*.}
  minimum_minor=${minimum_rest%%.*}
  minimum_patch=${minimum_rest#*.}

  if [ "$candidate_major" -ne "$minimum_major" ]; then
    [ "$candidate_major" -gt "$minimum_major" ]
  elif [ "$candidate_minor" -ne "$minimum_minor" ]; then
    [ "$candidate_minor" -gt "$minimum_minor" ]
  else
    [ "$candidate_patch" -ge "$minimum_patch" ]
  fi
}

if ! LIMA_VERSION_OUTPUT=$("$LIMACTL_PATH" --version 2>&1); then
  echo "Error: failed to run ${LIMACTL_PATH} --version" >&2
  printf '%s\n' "$LIMA_VERSION_OUTPUT" >&2
  show_lima_install_help
  exit 1
fi
if ! LIMA_VERSION=$(extract_lima_version "$LIMA_VERSION_OUTPUT"); then
  echo "Error: could not parse the Lima version reported by ${LIMACTL_PATH}: ${LIMA_VERSION_OUTPUT}" >&2
  show_lima_install_help
  exit 1
fi
if ! version_at_least "$LIMA_VERSION" "$MINIMUM_LIMA_VERSION"; then
  echo "Error: Lima ${LIMA_VERSION} is unsupported; watermelon requires Lima ${MINIMUM_LIMA_VERSION} or newer" >&2
  show_lima_install_help
  exit 1
fi

if ! LIMA_HOST_OS=$("$LIMACTL_PATH" info --yq '.hostOS'); then
  echo "Error: ${LIMACTL_PATH} info failed while checking the Lima host OS" >&2
  show_lima_install_help
  exit 1
fi
if [ "$LIMA_HOST_OS" != "$OS_NAME" ]; then
  echo "Error: Lima reported host OS ${LIMA_HOST_OS}, expected ${OS_NAME}" >&2
  show_lima_install_help
  exit 1
fi

if ! LIMA_HOST_ARCH=$("$LIMACTL_PATH" info --yq '.hostArch'); then
  echo "Error: ${LIMACTL_PATH} info failed while checking the Lima host architecture" >&2
  show_lima_install_help
  exit 1
fi
case "$LIMA_HOST_ARCH" in
  arm64|aarch64) SIDECAR_ARCH="arm64" ;;
  x86_64|amd64)  SIDECAR_ARCH="amd64" ;;
  *)
    echo "Error: unsupported Lima host architecture: ${LIMA_HOST_ARCH}" >&2
    exit 1
    ;;
esac

case "$OS_NAME" in
  darwin) REQUIRED_VM_TYPE="vz" ;;
  linux)  REQUIRED_VM_TYPE="qemu" ;;
esac
if ! LIMA_VM_TYPES=$("$LIMACTL_PATH" info --yq '.vmTypes[]'); then
  echo "Error: ${LIMACTL_PATH} info failed while checking available VM backends" >&2
  show_lima_install_help
  exit 1
fi
if ! printf '%s\n' "$LIMA_VM_TYPES" | grep -Fqx "$REQUIRED_VM_TYPE"; then
  echo "Error: Lima backend ${REQUIRED_VM_TYPE} is unavailable (reported backends: ${LIMA_VM_TYPES:-none})" >&2
  show_lima_install_help
  exit 1
fi

# Print the first word from Lima's QEMU_SYSTEM_* shell-word syntax without
# evaluating substitutions or commands. The complete value is still parsed so
# malformed quoting in later debugging arguments is rejected too.
first_shell_word() {
  case "$1" in
    *'
'*) return 1 ;;
  esac
  printf '%s\n' "$1" | awk '
    {
      input = $0
      state = "between"
      word = ""
      first = ""
      have_first = 0
      in_word = 0
      for (position = 1; position <= length(input); position++) {
        character = substr(input, position, 1)
        if (state == "between") {
          if (character == " " || character == "\t") continue
          in_word = 1
          if (character == "\047") state = "single"
          else if (character == "\042") state = "double"
          else if (character == "\\") state = "escape"
          else { state = "plain"; word = word character }
        } else if (state == "plain") {
          if (character == " " || character == "\t") {
            if (!have_first) { first = word; have_first = 1 }
            word = ""
            in_word = 0
            state = "between"
          } else if (character == "\047") state = "single"
          else if (character == "\042") state = "double"
          else if (character == "\\") state = "escape"
          else word = word character
        } else if (state == "single") {
          if (character == "\047") state = "plain"
          else word = word character
        } else if (state == "double") {
          if (character == "\042") state = "plain"
          else if (character == "\\") state = "double_escape"
          else word = word character
        } else if (state == "escape") {
          word = word character
          state = "plain"
        } else if (state == "double_escape") {
          word = word character
          state = "double"
        }
      }
      if (state == "single" || state == "double" || state == "escape" || state == "double_escape") exit 1
      if (in_word && !have_first) { first = word; have_first = 1 }
      if (!have_first || first == "") exit 1
      print first
    }
  '
}

# Lima's registered qemu backend still needs an architecture-appropriate QEMU
# system executable on Linux. Resolve QEMU_SYSTEM_* with Lima-compatible
# shell-word quoting: the first word is the executable and remaining words are
# debugging arguments used only when Lima launches the VM.
if [ "$OS_NAME" = "linux" ]; then
  case "$LIMA_HOST_ARCH" in
    arm64|aarch64)
      QEMU_OVERRIDE_NAME="QEMU_SYSTEM_AARCH64"
      QEMU_SYSTEM_COMMAND_DEFAULT=qemu-system-aarch64
      QEMU_OVERRIDE_VALUE=${QEMU_SYSTEM_AARCH64:-}
      ;;
    x86_64|amd64)
      QEMU_OVERRIDE_NAME="QEMU_SYSTEM_X86_64"
      QEMU_SYSTEM_COMMAND_DEFAULT=qemu-system-x86_64
      QEMU_OVERRIDE_VALUE=${QEMU_SYSTEM_X86_64:-}
      ;;
  esac
  if [ -n "$QEMU_OVERRIDE_VALUE" ]; then
    if ! QEMU_SYSTEM_COMMAND=$(first_shell_word "$QEMU_OVERRIDE_VALUE"); then
      echo "Error: ${QEMU_OVERRIDE_NAME} is not a valid Lima shell-word command: ${QEMU_OVERRIDE_VALUE}" >&2
      echo "Set ${QEMU_OVERRIDE_NAME} to an executable name, or quote a path containing spaces inside the value" >&2
      exit 1
    fi
  else
    QEMU_SYSTEM_COMMAND=$QEMU_SYSTEM_COMMAND_DEFAULT
  fi
  if ! QEMU_SYSTEM_PATH=$(command -v "$QEMU_SYSTEM_COMMAND" 2>/dev/null) \
    || [ ! -f "$QEMU_SYSTEM_PATH" ] || [ ! -x "$QEMU_SYSTEM_PATH" ]; then
    echo "Error: QEMU executable ${QEMU_SYSTEM_COMMAND} required by Lima for Linux/${LIMA_HOST_ARCH} was not found or is not executable" >&2
    echo "Install QEMU with your system package manager, or set ${QEMU_OVERRIDE_NAME} to a Lima shell-word command whose first word is the architecture-appropriate executable" >&2
    exit 1
  fi
  if ! QEMU_VERSION_OUTPUT=$("$QEMU_SYSTEM_PATH" --version 2>&1); then
    echo "Error: failed to run ${QEMU_SYSTEM_PATH} --version" >&2
    printf '%s\n' "$QEMU_VERSION_OUTPUT" >&2
    echo "Install a working QEMU system emulator, or correct ${QEMU_OVERRIDE_NAME}, then run the installer again" >&2
    exit 1
  fi
  case "$QEMU_VERSION_OUTPUT" in
    "QEMU emulator version "*) ;;
    *)
      echo "Error: ${QEMU_SYSTEM_PATH} --version did not identify a QEMU system emulator" >&2
      printf '%s\n' "$QEMU_VERSION_OUTPUT" >&2
      echo "Install a working QEMU system emulator, or correct ${QEMU_OVERRIDE_NAME}, then run the installer again" >&2
      exit 1
      ;;
  esac
fi

BINARY="watermelon-${OS_NAME}-${CLI_ARCH}"
SIDECAR="watermelon-nfqd-linux-${SIDECAR_ARCH}"

# Establish that the destination can be used before doing any network work.
USE_SUDO=false
if [ -e "$INSTALL_DIR" ] && [ ! -d "$INSTALL_DIR" ]; then
  echo "Error: ${INSTALL_DIR} exists but is not a directory" >&2
  exit 1
fi
if [ ! -d "$INSTALL_DIR" ]; then
  if mkdir -p "$INSTALL_DIR" 2>/dev/null; then
    :
  elif command -v sudo >/dev/null 2>&1; then
    echo "Creating ${INSTALL_DIR} (requires sudo)..."
    if ! sudo -v; then
      echo "Error: sudo access is required to create ${INSTALL_DIR}; set WATERMELON_INSTALL_DIR to a writable absolute path" >&2
      exit 1
    fi
    if ! sudo mkdir -p "$INSTALL_DIR"; then
      echo "Error: ${INSTALL_DIR} could not be created with sudo; set WATERMELON_INSTALL_DIR to a writable absolute path" >&2
      exit 1
    fi
  else
    echo "Error: ${INSTALL_DIR} does not exist and cannot be created; set WATERMELON_INSTALL_DIR to a writable absolute path" >&2
    exit 1
  fi
fi

if [ ! -w "$INSTALL_DIR" ]; then
  if ! command -v sudo >/dev/null 2>&1; then
    echo "Error: ${INSTALL_DIR} is not writable and sudo is unavailable; set WATERMELON_INSTALL_DIR to a writable absolute path" >&2
    exit 1
  fi
  echo "Checking write access to ${INSTALL_DIR} (requires sudo)..."
  if ! sudo -v; then
    echo "Error: sudo access is required to install into ${INSTALL_DIR}; set WATERMELON_INSTALL_DIR to a writable absolute path" >&2
    exit 1
  fi
  USE_SUDO=true
fi

echo "Using Lima ${LIMA_VERSION} at ${LIMACTL_PATH}"
echo "Downloading watermelon for ${OS_NAME}/${CLI_ARCH}..."

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
  echo "Downloading watermelon network interceptor for linux/${SIDECAR_ARCH}..."
  SIDECAR_TMP="$TMP_ROOT/$SIDECAR"
  curl -fsSL -o "$SIDECAR_TMP" "$SIDECAR_URL"
else
  echo "Warning: release sidecar ${SIDECAR} not found; ask-mode will require WATERMELON_NFQD_BINARY" >&2
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
echo "Running watermelon doctor..."
"${INSTALL_DIR}/watermelon" doctor

case ":${PATH:-}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    echo "Warning: ${INSTALL_DIR} is not in PATH; add it before running watermelon by name" >&2
    ;;
esac
