#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  echo "usage: $0 ASSET_DIR EXPECTED_VERSION" >&2
  exit 2
fi

case "$1" in
  /*) asset_dir=$1 ;;
  *) asset_dir=$(CDPATH='' cd "$1" && pwd -P) ;;
esac
expected_version=$2
unset QEMU_SYSTEM_AARCH64 QEMU_SYSTEM_X86_64
script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
repo_root=$(CDPATH='' cd "$script_dir/.." && pwd -P)

installer_minimum_lima_version=$(sed -n 's/^MINIMUM_LIMA_VERSION="\([^"]*\)"$/\1/p' "$repo_root/install.sh")
go_minimum_lima_version=$(sed -n 's/^const MinimumSupportedVersion = "\([^"]*\)"$/\1/p' "$repo_root/internal/lima/info.go")
if [ -z "$installer_minimum_lima_version" ] || [ "$installer_minimum_lima_version" != "$go_minimum_lima_version" ]; then
  echo "installer Lima minimum (${installer_minimum_lima_version:-missing}) does not match Go compatibility minimum (${go_minimum_lima_version:-missing})" >&2
  exit 1
fi
installer_minimum_macos_version=$(sed -n 's/^MINIMUM_MACOS_VERSION="\([^"]*\)"$/\1/p' "$repo_root/install.sh")
go_minimum_macos_version=$(sed -n 's/^[[:space:]]*minimumSupportedMacOSMajor[[:space:]]*=[[:space:]]*\([0-9][0-9]*\)$/\1/p' "$repo_root/internal/cli/doctor.go")
if [ -z "$installer_minimum_macos_version" ] || [ "$installer_minimum_macos_version" != "$go_minimum_macos_version" ]; then
  echo "installer macOS minimum (${installer_minimum_macos_version:-missing}) does not match doctor minimum (${go_minimum_macos_version:-missing})" >&2
  exit 1
fi

assets="
watermelon-darwin-arm64
watermelon-darwin-amd64
watermelon-linux-arm64
watermelon-linux-amd64
watermelon-nfqd-linux-arm64
watermelon-nfqd-linux-amd64
"

for asset in $assets; do
  if [ ! -s "$asset_dir/$asset" ] || [ ! -x "$asset_dir/$asset" ]; then
    echo "release asset is missing, empty, or not executable: $asset" >&2
    exit 1
  fi
done

case "$(uname -s):$(uname -m)" in
  Darwin:arm64)
    native_asset=watermelon-darwin-arm64
    native_sidecar=watermelon-nfqd-linux-arm64
    lima_host_os=darwin
    lima_host_arch=arm64
    lima_vm_type=vz
    ;;
  Darwin:x86_64)
    native_asset=watermelon-darwin-amd64
    native_sidecar=watermelon-nfqd-linux-amd64
    lima_host_os=darwin
    lima_host_arch=x86_64
    lima_vm_type=vz
    ;;
  Linux:aarch64|Linux:arm64)
    native_asset=watermelon-linux-arm64
    native_sidecar=watermelon-nfqd-linux-arm64
    lima_host_os=linux
    lima_host_arch=aarch64
    lima_vm_type=qemu
    run_sidecar=true
    ;;
  Linux:x86_64)
    native_asset=watermelon-linux-amd64
    native_sidecar=watermelon-nfqd-linux-amd64
    lima_host_os=linux
    lima_host_arch=x86_64
    lima_vm_type=qemu
    run_sidecar=true
    ;;
  *)
    echo "unsupported smoke-test host: $(uname -s)/$(uname -m)" >&2
    exit 1
    ;;
esac

smoke_root=$(mktemp -d "${TMPDIR:-/tmp}/watermelon-install-smoke.XXXXXX")
case "$smoke_root" in
  "${TMPDIR:-/tmp}"/watermelon-install-smoke.*) ;;
  *) echo "refusing unsafe temporary path: $smoke_root" >&2; exit 1 ;;
esac
cleanup() {
  rm -rf -- "$smoke_root"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM

fake_bin="$smoke_root/fake-bin"
install_dir="$smoke_root/install dir"
installer_tmp="$smoke_root/tmp"
installer_home="$smoke_root/home"
curl_log="$smoke_root/curl.log"
limactl_log="$smoke_root/limactl.log"
qemu_log="$smoke_root/qemu.log"
lima_home="$smoke_root/lima-home"
mkdir -p "$fake_bin" "$installer_tmp" "$installer_home" "$lima_home"
: > "$curl_log"
: > "$limactl_log"
: > "$qemu_log"

cat > "$fake_bin/curl" <<'EOF'
#!/bin/sh
set -eu

output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      output=$2
      shift 2
      ;;
    -*) shift ;;
    *)
      url=$1
      shift
      ;;
  esac
done

if [ -z "$url" ]; then
  echo "curl smoke stub received no URL" >&2
  exit 1
fi
printf '%s\n' "$url" >> "$WM_SMOKE_CURL_LOG"

emit_release() {
  printf '%s\n' '{"assets":['
  separator=
  for asset in \
    watermelon-darwin-arm64 \
    watermelon-darwin-amd64 \
    watermelon-linux-arm64 \
    watermelon-linux-amd64 \
    watermelon-nfqd-linux-arm64 \
    watermelon-nfqd-linux-amd64
  do
    printf '%s{"browser_download_url":"file://%s/%s"}\n' "$separator" "$WM_SMOKE_ASSET_DIR" "$asset"
    separator=,
  done
  printf '%s\n' ']}'
}

case "$url" in
  https://api.github.com/repos/SomosPollo/watermelon/releases/latest)
    if [ -n "$output" ]; then
      emit_release > "$output"
    else
      emit_release
    fi
    ;;
  file://*)
    if [ -z "$output" ]; then
      echo "curl smoke stub received no output path for $url" >&2
      exit 1
    fi
    cp "${url#file://}" "$output"
    ;;
  *)
    echo "curl smoke stub received unexpected URL: $url" >&2
    exit 1
    ;;
esac
EOF

cat > "$fake_bin/limactl" <<'EOF'
#!/bin/sh
set -eu

printf '%s\n' "$*" >> "$WM_SMOKE_LIMACTL_LOG"

case "${1:-}" in
  --version)
    printf 'limactl version %s\n' "${WM_SMOKE_LIMA_VERSION:-2.0.0}"
    ;;
  info)
    if [ "${WM_SMOKE_LIMA_INFO_FAIL:-false}" = true ]; then
      echo "simulated limactl info failure" >&2
      exit 1
    fi
    if [ "${2:-}" = "--yq" ]; then
      case "${3:-}" in
        .hostOS) printf '%s\n' "$WM_SMOKE_LIMA_HOST_OS" ;;
        .hostArch) printf '%s\n' "$WM_SMOKE_LIMA_HOST_ARCH" ;;
        '.vmTypes[]') printf '%s\n' "$WM_SMOKE_LIMA_VM_TYPE" ;;
        *) echo "limactl smoke stub received unexpected info query: ${3:-}" >&2; exit 1 ;;
      esac
    else
      printf '{"version":"v%s","hostOS":"%s","hostArch":"%s","limaHome":"%s","vmTypes":["%s"],"vmTypesEx":{"%s":{"location":"internal"}}}\n' \
        "${WM_SMOKE_LIMA_VERSION:-2.0.0}" \
        "$WM_SMOKE_LIMA_HOST_OS" \
        "$WM_SMOKE_LIMA_HOST_ARCH" \
        "$WM_SMOKE_LIMA_HOME" \
        "$WM_SMOKE_LIMA_VM_TYPE" \
        "$WM_SMOKE_LIMA_VM_TYPE"
    fi
    ;;
  list)
    # limactl emits no records when the instance store is empty.
    :
    ;;
  *)
    echo "limactl smoke stub received unexpected arguments: $*" >&2
    exit 1
    ;;
esac
EOF

for qemu_name in qemu-system-aarch64 qemu-system-x86_64; do
  cat > "$fake_bin/$qemu_name" <<'EOF'
#!/bin/sh
set -eu

qemu_name=${0##*/}
printf '%s %s\n' "$qemu_name" "$*" >> "$WM_SMOKE_QEMU_LOG"
if [ "${1:-}" != "--version" ]; then
  echo "$qemu_name smoke stub received unexpected arguments: $*" >&2
  exit 1
fi
if [ "${WM_SMOKE_QEMU_FAIL:-false}" = true ]; then
  echo "simulated $qemu_name failure" >&2
  exit 1
fi
printf '%s\n' "${WM_SMOKE_QEMU_VERSION_OUTPUT:-QEMU emulator version 9.2.0}"
EOF
  chmod +x "$fake_bin/$qemu_name"
done

cat > "$fake_bin/sudo" <<'EOF'
#!/bin/sh
echo "installer unexpectedly invoked sudo: $*" >&2
exit 1
EOF

chmod +x "$fake_bin/curl" "$fake_bin/limactl" "$fake_bin/sudo"

# Exercise the macOS product-version preflight even when this smoke test runs
# on Linux. Its uname and sw_vers stubs let each case fail before Lima or curl.
platform_bin="$smoke_root/platform-bin"
sw_vers_log="$smoke_root/sw-vers.log"
mkdir -p "$platform_bin"
cat > "$platform_bin/uname" <<'EOF'
#!/bin/sh
set -eu

case "${1:-}" in
  -m) printf '%s\n' "${WM_SMOKE_UNAME_ARCH:-arm64}" ;;
  -s) printf '%s\n' "${WM_SMOKE_UNAME_OS:-Darwin}" ;;
  *) echo "uname smoke stub received unexpected arguments: $*" >&2; exit 1 ;;
esac
EOF
cat > "$platform_bin/sw_vers" <<'EOF'
#!/bin/sh
set -eu

printf '%s\n' "$*" >> "$WM_SMOKE_SW_VERS_LOG"
if [ "${1:-}" != "-productVersion" ]; then
  echo "sw_vers smoke stub received unexpected arguments: $*" >&2
  exit 1
fi
if [ "${WM_SMOKE_SW_VERS_FAIL:-false}" = true ]; then
  echo "simulated sw_vers failure" >&2
  exit 1
fi
printf '%s\n' "${WM_SMOKE_MACOS_VERSION:-13.0}"
EOF
for platform_tool in curl mktemp install ssh awk; do
  ln -s "$(command -v "$platform_tool")" "$platform_bin/$platform_tool"
done
chmod +x "$platform_bin/uname" "$platform_bin/sw_vers"

: > "$curl_log"
: > "$sw_vers_log"
if PATH="$platform_bin" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_MACOS_VERSION="12.7.6" \
  WM_SMOKE_SW_VERS_LOG="$sw_vers_log" \
  /bin/sh "$repo_root/install.sh" > "$smoke_root/old-macos-output.log" 2>&1; then
  echo "installer accepted unsupported macOS 12.7.6" >&2
  exit 1
fi
grep -Fq "requires macOS ${installer_minimum_macos_version} or newer" "$smoke_root/old-macos-output.log"
grep -Fqx -- "-productVersion" "$sw_vers_log"
if [ -s "$curl_log" ]; then
  echo "installer downloaded assets before rejecting unsupported macOS" >&2
  exit 1
fi

: > "$sw_vers_log"
if PATH="$platform_bin" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_MACOS_VERSION="13.beta" \
  WM_SMOKE_SW_VERS_LOG="$sw_vers_log" \
  /bin/sh "$repo_root/install.sh" > "$smoke_root/malformed-macos-output.log" 2>&1; then
  echo "installer accepted malformed macOS product version" >&2
  exit 1
fi
grep -Fq "could not parse the macOS product version" "$smoke_root/malformed-macos-output.log"

: > "$sw_vers_log"
if PATH="$platform_bin" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_SW_VERS_FAIL=true \
  WM_SMOKE_SW_VERS_LOG="$sw_vers_log" \
  /bin/sh "$repo_root/install.sh" > "$smoke_root/unreadable-macos-output.log" 2>&1; then
  echo "installer accepted an unreadable macOS product version" >&2
  exit 1
fi
grep -Fq "failed to read the macOS product version" "$smoke_root/unreadable-macos-output.log"

# A well-formed minimum macOS version reaches the later Lima check.
: > "$sw_vers_log"
if PATH="$platform_bin" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_MACOS_VERSION="${installer_minimum_macos_version}.0.1" \
  WM_SMOKE_SW_VERS_LOG="$sw_vers_log" \
  /bin/sh "$repo_root/install.sh" > "$smoke_root/current-macos-output.log" 2>&1; then
  echo "installer unexpectedly succeeded without limactl" >&2
  exit 1
fi
grep -Fq "limactl was not found in PATH" "$smoke_root/current-macos-output.log"

# Linux must never invoke the macOS-only sw_vers probe.
: > "$sw_vers_log"
if PATH="$platform_bin" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_UNAME_OS=Linux \
  WM_SMOKE_UNAME_ARCH=x86_64 \
  WM_SMOKE_SW_VERS_FAIL=true \
  WM_SMOKE_SW_VERS_LOG="$sw_vers_log" \
  /bin/sh "$repo_root/install.sh" > "$smoke_root/linux-platform-output.log" 2>&1; then
  echo "installer unexpectedly succeeded without limactl" >&2
  exit 1
fi
grep -Fq "limactl was not found in PATH" "$smoke_root/linux-platform-output.log"
if [ -s "$sw_vers_log" ]; then
  echo "installer invoked sw_vers on Linux" >&2
  exit 1
fi

# curl is installer-only, but it must be rejected before Lima is probed or the
# destination is changed.
: > "$limactl_log"
no_curl_bin="$smoke_root/no-curl-bin"
mkdir -p "$no_curl_bin"
ln -s "$fake_bin/limactl" "$no_curl_bin/limactl"
for required_tool in uname mktemp install ssh awk; do
  ln -s "$(command -v "$required_tool")" "$no_curl_bin/$required_tool"
done
if [ "$lima_host_os" = darwin ]; then
  ln -s "$(command -v sw_vers)" "$no_curl_bin/sw_vers"
fi
if PATH="$no_curl_bin" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_LIMACTL_LOG="$limactl_log" \
  /bin/sh "$repo_root/install.sh" > "$smoke_root/missing-curl-output.log" 2>&1; then
  echo "installer accepted a PATH without curl" >&2
  exit 1
fi
grep -Fq "required command not found: curl" "$smoke_root/missing-curl-output.log"
if [ -s "$limactl_log" ]; then
  echo "installer probed Lima before reporting missing curl" >&2
  exit 1
fi

# ssh is a runtime dependency and must be rejected before Lima or network use.
: > "$curl_log"
: > "$limactl_log"
no_ssh_bin="$smoke_root/no-ssh-bin"
mkdir -p "$no_ssh_bin"
ln -s "$fake_bin/curl" "$no_ssh_bin/curl"
ln -s "$fake_bin/limactl" "$no_ssh_bin/limactl"
for required_tool in uname mktemp install; do
  ln -s "$(command -v "$required_tool")" "$no_ssh_bin/$required_tool"
done
if [ "$lima_host_os" = darwin ]; then
  ln -s "$(command -v sw_vers)" "$no_ssh_bin/sw_vers"
fi
if PATH="$no_ssh_bin" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_CURL_LOG="$curl_log" \
  WM_SMOKE_LIMACTL_LOG="$limactl_log" \
  /bin/sh "$repo_root/install.sh" > "$smoke_root/missing-ssh-output.log" 2>&1; then
  echo "installer accepted a PATH without ssh" >&2
  exit 1
fi
grep -Fq "required command not found: ssh" "$smoke_root/missing-ssh-output.log"
grep -Fq "OpenSSH" "$smoke_root/missing-ssh-output.log"
if [ -s "$curl_log" ] || [ -s "$limactl_log" ]; then
  echo "installer probed Lima or downloaded assets before reporting missing ssh" >&2
  exit 1
fi

# Incompatible or non-operational Lima installations must fail before the
# installer contacts GitHub.
: > "$curl_log"
no_lima_bin="$smoke_root/no-lima-bin"
mkdir -p "$no_lima_bin"
ln -s "$fake_bin/curl" "$no_lima_bin/curl"
for required_tool in uname mktemp install ssh awk; do
  ln -s "$(command -v "$required_tool")" "$no_lima_bin/$required_tool"
done
if [ "$lima_host_os" = darwin ]; then
  ln -s "$(command -v sw_vers)" "$no_lima_bin/sw_vers"
fi
if PATH="$no_lima_bin" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_CURL_LOG="$curl_log" \
  /bin/sh "$repo_root/install.sh" > "$smoke_root/missing-lima-output.log" 2>&1; then
  echo "installer accepted a PATH without limactl" >&2
  exit 1
fi
grep -Fq "limactl was not found in PATH" "$smoke_root/missing-lima-output.log"
if [ -s "$curl_log" ]; then
  echo "installer downloaded assets before reporting missing limactl" >&2
  exit 1
fi

: > "$curl_log"
if PATH="$fake_bin:$PATH" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_ASSET_DIR="$asset_dir" \
  WM_SMOKE_CURL_LOG="$curl_log" \
  WM_SMOKE_LIMACTL_LOG="$limactl_log" \
  WM_SMOKE_LIMA_VERSION="1.2.3" \
  WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
  WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
  WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
  WM_SMOKE_LIMA_HOME="$lima_home" \
  sh "$repo_root/install.sh" > "$smoke_root/old-lima-output.log" 2>&1; then
  echo "installer accepted unsupported Lima 1.2.3" >&2
  exit 1
fi
grep -Fq "requires Lima ${installer_minimum_lima_version} or newer" "$smoke_root/old-lima-output.log"
if [ -s "$curl_log" ]; then
  echo "installer downloaded assets before rejecting unsupported Lima" >&2
  exit 1
fi

: > "$curl_log"
if PATH="$fake_bin:$PATH" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_ASSET_DIR="$asset_dir" \
  WM_SMOKE_CURL_LOG="$curl_log" \
  WM_SMOKE_LIMACTL_LOG="$limactl_log" \
  WM_SMOKE_LIMA_VERSION="2.0" \
  WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
  WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
  WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
  WM_SMOKE_LIMA_HOME="$lima_home" \
  sh "$repo_root/install.sh" > "$smoke_root/malformed-lima-output.log" 2>&1; then
  echo "installer accepted malformed Lima version 2.0" >&2
  exit 1
fi
grep -Fq "could not parse the Lima version" "$smoke_root/malformed-lima-output.log"
if [ -s "$curl_log" ]; then
  echo "installer downloaded assets before rejecting a malformed Lima version" >&2
  exit 1
fi

: > "$curl_log"
if PATH="$fake_bin:$PATH" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_ASSET_DIR="$asset_dir" \
  WM_SMOKE_CURL_LOG="$curl_log" \
  WM_SMOKE_LIMACTL_LOG="$limactl_log" \
  WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
  WM_SMOKE_LIMA_INFO_FAIL=true \
  WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
  WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
  WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
  WM_SMOKE_LIMA_HOME="$lima_home" \
  sh "$repo_root/install.sh" > "$smoke_root/broken-lima-output.log" 2>&1; then
  echo "installer accepted a non-operational Lima installation" >&2
  exit 1
fi
grep -Fq "info failed" "$smoke_root/broken-lima-output.log"
if [ -s "$curl_log" ]; then
  echo "installer downloaded assets before rejecting non-operational Lima" >&2
  exit 1
fi

: > "$curl_log"
case "$lima_vm_type" in
  qemu) wrong_lima_vm_type=vz ;;
  *) wrong_lima_vm_type=qemu ;;
esac
if PATH="$fake_bin:$PATH" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_ASSET_DIR="$asset_dir" \
  WM_SMOKE_CURL_LOG="$curl_log" \
  WM_SMOKE_LIMACTL_LOG="$limactl_log" \
  WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
  WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
  WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
  WM_SMOKE_LIMA_VM_TYPE="$wrong_lima_vm_type" \
  WM_SMOKE_LIMA_HOME="$lima_home" \
  sh "$repo_root/install.sh" > "$smoke_root/missing-backend-output.log" 2>&1; then
  echo "installer accepted Lima without the required ${lima_vm_type} backend" >&2
  exit 1
fi
grep -Fq "backend ${lima_vm_type} is unavailable" "$smoke_root/missing-backend-output.log"
if [ -s "$curl_log" ]; then
  echo "installer downloaded assets before rejecting a missing Lima backend" >&2
  exit 1
fi

if [ "$lima_host_os" = linux ]; then
  case "$lima_host_arch" in
    arm64|aarch64)
      native_qemu_name=qemu-system-aarch64
      native_qemu_override=QEMU_SYSTEM_AARCH64
      ;;
    x86_64|amd64)
      native_qemu_name=qemu-system-x86_64
      native_qemu_override=QEMU_SYSTEM_X86_64
      ;;
  esac

  # A registered qemu VM type is insufficient when its system executable is
  # absent. This must fail before destination creation or downloads.
  no_qemu_bin="$smoke_root/no-qemu-bin"
  mkdir -p "$no_qemu_bin"
  ln -s "$fake_bin/curl" "$no_qemu_bin/curl"
  ln -s "$fake_bin/limactl" "$no_qemu_bin/limactl"
  for required_tool in uname mktemp install ssh grep awk; do
    ln -s "$(command -v "$required_tool")" "$no_qemu_bin/$required_tool"
  done
  : > "$curl_log"
  : > "$qemu_log"
  if PATH="$no_qemu_bin" \
    HOME="$installer_home" \
    TMPDIR="$installer_tmp" \
    WATERMELON_INSTALL_DIR="$install_dir" \
    WM_SMOKE_CURL_LOG="$curl_log" \
    WM_SMOKE_LIMACTL_LOG="$limactl_log" \
    WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
    WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
    WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
    WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
    WM_SMOKE_LIMA_HOME="$lima_home" \
    /bin/sh "$repo_root/install.sh" > "$smoke_root/missing-qemu-output.log" 2>&1; then
    echo "installer accepted Linux without $native_qemu_name" >&2
    exit 1
  fi
  grep -Fq "QEMU executable ${native_qemu_name}" "$smoke_root/missing-qemu-output.log"
  grep -Fq "set ${native_qemu_override}" "$smoke_root/missing-qemu-output.log"
  if [ -s "$curl_log" ]; then
    echo "installer downloaded assets before reporting missing QEMU" >&2
    exit 1
  fi

  # Lima's architecture-specific override is parsed as shell words without
  # evaluation. Embedded quotes preserve a space in the executable path, and
  # trailing debugging arguments do not become part of the command name.
  custom_qemu="$smoke_root/custom qemu"
  custom_qemu_override="\"$custom_qemu\" -display none"
  custom_qemu_destination="$smoke_root/qemu-override-destination"
  cat > "$custom_qemu" <<'EOF'
#!/bin/sh
set -eu

printf 'custom qemu %s\n' "$*" >> "$WM_SMOKE_QEMU_LOG"
[ "${1:-}" = "--version" ] || exit 1
printf 'QEMU emulator version 9.2.0\n'
EOF
  chmod +x "$custom_qemu"
  : > "$custom_qemu_destination"
  : > "$curl_log"
  : > "$qemu_log"
  env_path=$(command -v env)
  if "$env_path" \
    "$native_qemu_override=$custom_qemu_override" \
    PATH="$no_qemu_bin" \
    HOME="$installer_home" \
    TMPDIR="$installer_tmp" \
    WATERMELON_INSTALL_DIR="$custom_qemu_destination" \
    WM_SMOKE_CURL_LOG="$curl_log" \
    WM_SMOKE_LIMACTL_LOG="$limactl_log" \
    WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
    WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
    WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
    WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
    WM_SMOKE_LIMA_HOME="$lima_home" \
    WM_SMOKE_QEMU_LOG="$qemu_log" \
    /bin/sh "$repo_root/install.sh" > "$smoke_root/qemu-override-output.log" 2>&1; then
    echo "installer unexpectedly accepted a non-directory destination" >&2
    exit 1
  fi
  grep -Fq "exists but is not a directory" "$smoke_root/qemu-override-output.log"
  grep -Fqx "custom qemu --version" "$qemu_log"
  if [ -s "$curl_log" ]; then
    echo "installer downloaded assets during QEMU override preflight" >&2
    exit 1
  fi

  : > "$curl_log"
  : > "$qemu_log"
  if "$env_path" \
    "$native_qemu_override='unterminated" \
    PATH="$no_qemu_bin" \
    HOME="$installer_home" \
    TMPDIR="$installer_tmp" \
    WATERMELON_INSTALL_DIR="$install_dir" \
    WM_SMOKE_CURL_LOG="$curl_log" \
    WM_SMOKE_LIMACTL_LOG="$limactl_log" \
    WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
    WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
    WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
    WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
    WM_SMOKE_LIMA_HOME="$lima_home" \
    WM_SMOKE_QEMU_LOG="$qemu_log" \
    /bin/sh "$repo_root/install.sh" > "$smoke_root/malformed-qemu-override-output.log" 2>&1; then
    echo "installer accepted a malformed QEMU override" >&2
    exit 1
  fi
  grep -Fq "not a valid Lima shell-word command" "$smoke_root/malformed-qemu-override-output.log"
  if [ -s "$curl_log" ] || [ -s "$qemu_log" ]; then
    echo "installer used QEMU or downloaded assets after rejecting a malformed override" >&2
    exit 1
  fi

  : > "$curl_log"
  : > "$qemu_log"
  if PATH="$fake_bin:$PATH" \
    HOME="$installer_home" \
    TMPDIR="$installer_tmp" \
    WATERMELON_INSTALL_DIR="$install_dir" \
    WM_SMOKE_CURL_LOG="$curl_log" \
    WM_SMOKE_LIMACTL_LOG="$limactl_log" \
    WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
    WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
    WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
    WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
    WM_SMOKE_LIMA_HOME="$lima_home" \
    WM_SMOKE_QEMU_LOG="$qemu_log" \
    WM_SMOKE_QEMU_FAIL=true \
    sh "$repo_root/install.sh" > "$smoke_root/broken-qemu-output.log" 2>&1; then
    echo "installer accepted a QEMU executable whose --version failed" >&2
    exit 1
  fi
  grep -Fq "failed to run $fake_bin/$native_qemu_name --version" "$smoke_root/broken-qemu-output.log"
  grep -Fqx "$native_qemu_name --version" "$qemu_log"
  if [ -s "$curl_log" ]; then
    echo "installer downloaded assets before rejecting broken QEMU" >&2
    exit 1
  fi

  : > "$curl_log"
  : > "$qemu_log"
  if PATH="$fake_bin:$PATH" \
    HOME="$installer_home" \
    TMPDIR="$installer_tmp" \
    WATERMELON_INSTALL_DIR="$install_dir" \
    WM_SMOKE_CURL_LOG="$curl_log" \
    WM_SMOKE_LIMACTL_LOG="$limactl_log" \
    WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
    WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
    WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
    WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
    WM_SMOKE_LIMA_HOME="$lima_home" \
    WM_SMOKE_QEMU_LOG="$qemu_log" \
    WM_SMOKE_QEMU_VERSION_OUTPUT="not a QEMU executable" \
    sh "$repo_root/install.sh" > "$smoke_root/wrong-qemu-output.log" 2>&1; then
    echo "installer accepted a non-QEMU executable" >&2
    exit 1
  fi
  grep -Fq "did not identify a QEMU system emulator" "$smoke_root/wrong-qemu-output.log"
  if [ -s "$curl_log" ]; then
    echo "installer downloaded assets before rejecting a non-QEMU executable" >&2
    exit 1
  fi
fi

# A missing destination that cannot be created directly must fail cleanly when
# sudo is absent, without contacting GitHub.
no_sudo_bin="$smoke_root/no-sudo-bin"
mkdir -p "$no_sudo_bin"
for fake_tool in curl limactl qemu-system-aarch64 qemu-system-x86_64; do
  ln -s "$fake_bin/$fake_tool" "$no_sudo_bin/$fake_tool"
done
for required_tool in uname mktemp install ssh grep awk; do
  ln -s "$(command -v "$required_tool")" "$no_sudo_bin/$required_tool"
done
cat > "$no_sudo_bin/mkdir" <<'EOF'
#!/bin/sh
exit 1
EOF
chmod +x "$no_sudo_bin/mkdir"
if [ "$lima_host_os" = darwin ]; then
  ln -s "$(command -v sw_vers)" "$no_sudo_bin/sw_vers"
fi
: > "$curl_log"
if PATH="$no_sudo_bin" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$smoke_root/no-sudo-destination" \
  WM_SMOKE_CURL_LOG="$curl_log" \
  WM_SMOKE_LIMACTL_LOG="$limactl_log" \
  WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
  WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
  WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
  WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
  WM_SMOKE_LIMA_HOME="$lima_home" \
  WM_SMOKE_QEMU_LOG="$qemu_log" \
  /bin/sh "$repo_root/install.sh" > "$smoke_root/no-sudo-output.log" 2>&1; then
  echo "installer accepted an uncreatable destination without sudo" >&2
  exit 1
fi
grep -Fq "cannot be created" "$smoke_root/no-sudo-output.log"
grep -Fq "WATERMELON_INSTALL_DIR" "$smoke_root/no-sudo-output.log"
if [ -s "$curl_log" ]; then
  echo "installer downloaded assets before reporting unavailable sudo" >&2
  exit 1
fi

# If direct destination creation fails, sudo credentials are checked first and
# both credential and mkdir failures get actionable install-directory guidance.
sudo_fail_bin="$smoke_root/sudo-fail-bin"
sudo_log="$smoke_root/sudo.log"
mkdir -p "$sudo_fail_bin"
cat > "$sudo_fail_bin/mkdir" <<'EOF'
#!/bin/sh
exit 1
EOF
cat > "$sudo_fail_bin/sudo" <<'EOF'
#!/bin/sh
set -eu

printf '%s\n' "$*" >> "$WM_SMOKE_SUDO_LOG"
case "${1:-}" in
  -v)
    if [ "${WM_SMOKE_SUDO_VALIDATE_FAIL:-false}" = true ]; then
      exit 1
    fi
    ;;
  mkdir)
    exit 1
    ;;
  *)
    echo "sudo smoke stub received unexpected arguments: $*" >&2
    exit 1
    ;;
esac
EOF
chmod +x "$sudo_fail_bin/mkdir" "$sudo_fail_bin/sudo"

: > "$curl_log"
: > "$sudo_log"
if PATH="$sudo_fail_bin:$fake_bin:$PATH" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$smoke_root/sudo-validation-destination" \
  WM_SMOKE_ASSET_DIR="$asset_dir" \
  WM_SMOKE_CURL_LOG="$curl_log" \
  WM_SMOKE_LIMACTL_LOG="$limactl_log" \
  WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
  WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
  WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
  WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
  WM_SMOKE_LIMA_HOME="$lima_home" \
  WM_SMOKE_QEMU_LOG="$qemu_log" \
  WM_SMOKE_SUDO_LOG="$sudo_log" \
  WM_SMOKE_SUDO_VALIDATE_FAIL=true \
  sh "$repo_root/install.sh" > "$smoke_root/sudo-validation-output.log" 2>&1; then
  echo "installer accepted failed sudo validation" >&2
  exit 1
fi
grep -Fq "sudo access is required to create" "$smoke_root/sudo-validation-output.log"
grep -Fqx -- "-v" "$sudo_log"
if [ -s "$curl_log" ]; then
  echo "installer downloaded assets before failed sudo validation" >&2
  exit 1
fi

: > "$curl_log"
: > "$sudo_log"
if PATH="$sudo_fail_bin:$fake_bin:$PATH" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$smoke_root/sudo-mkdir-destination" \
  WM_SMOKE_ASSET_DIR="$asset_dir" \
  WM_SMOKE_CURL_LOG="$curl_log" \
  WM_SMOKE_LIMACTL_LOG="$limactl_log" \
  WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
  WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
  WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
  WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
  WM_SMOKE_LIMA_HOME="$lima_home" \
  WM_SMOKE_QEMU_LOG="$qemu_log" \
  WM_SMOKE_SUDO_LOG="$sudo_log" \
  sh "$repo_root/install.sh" > "$smoke_root/sudo-mkdir-output.log" 2>&1; then
  echo "installer accepted failed sudo destination creation" >&2
  exit 1
fi
grep -Fq "could not be created with sudo" "$smoke_root/sudo-mkdir-output.log"
grep -Fqx -- "-v" "$sudo_log"
grep -Fqx "mkdir -p $smoke_root/sudo-mkdir-destination" "$sudo_log"
if [ -s "$curl_log" ]; then
  echo "installer downloaded assets before failed sudo destination creation" >&2
  exit 1
fi

: > "$curl_log"
: > "$limactl_log"
: > "$qemu_log"

PATH="$fake_bin:$PATH" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_ASSET_DIR="$asset_dir" \
  WM_SMOKE_CURL_LOG="$curl_log" \
  WM_SMOKE_LIMACTL_LOG="$limactl_log" \
  WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
  WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
  WM_SMOKE_LIMA_HOST_ARCH="$lima_host_arch" \
  WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
  WM_SMOKE_LIMA_HOME="$lima_home" \
  WM_SMOKE_QEMU_LOG="$qemu_log" \
  sh "$repo_root/install.sh" > "$smoke_root/installer-output.log"

installed_cli="$install_dir/watermelon"
installed_sidecar="$install_dir/$native_sidecar"
for installed in "$installed_cli" "$installed_sidecar"; do
  if [ ! -s "$installed" ] || [ ! -x "$installed" ]; then
    echo "installer output is missing, empty, or not executable: $installed" >&2
    exit 1
  fi
done
cmp "$asset_dir/$native_asset" "$installed_cli"
cmp "$asset_dir/$native_sidecar" "$installed_sidecar"
grep -Fqx "https://api.github.com/repos/SomosPollo/watermelon/releases/latest" "$curl_log"
grep -Fqx "file://$asset_dir/$native_asset" "$curl_log"
grep -Fqx "file://$asset_dir/$native_sidecar" "$curl_log"
grep -Fqx -- "--version" "$limactl_log"
grep -Fqx "info --yq .hostOS" "$limactl_log"
grep -Fqx "info --yq .hostArch" "$limactl_log"
grep -Fqx "info --yq .vmTypes[]" "$limactl_log"
# The installer preflight uses targeted --yq queries; plain info proves that the
# exact installed watermelon binary also ran its doctor command.
grep -Fqx "info" "$limactl_log"
if [ "$lima_host_os" = linux ]; then
  grep -Fqx "$native_qemu_name --version" "$qemu_log"
fi

# The CLI follows the process architecture while the Linux sidecar follows
# Lima's host architecture. Keeping those inputs separate is required when the
# installer itself runs through Rosetta on Apple silicon.
case "$lima_host_arch" in
  arm64|aarch64)
    alternate_lima_host_arch=x86_64
    alternate_sidecar=watermelon-nfqd-linux-amd64
    alternate_qemu_name=qemu-system-x86_64
    ;;
  *)
    alternate_lima_host_arch=arm64
    alternate_sidecar=watermelon-nfqd-linux-arm64
    alternate_qemu_name=qemu-system-aarch64
    ;;
esac
split_install_dir="$smoke_root/split-arch-install"
mkdir -p "$split_install_dir"
: > "$curl_log"
PATH="$fake_bin:$PATH" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$split_install_dir" \
  WM_SMOKE_ASSET_DIR="$asset_dir" \
  WM_SMOKE_CURL_LOG="$curl_log" \
  WM_SMOKE_LIMACTL_LOG="$limactl_log" \
  WM_SMOKE_LIMA_VERSION="$installer_minimum_lima_version" \
  WM_SMOKE_LIMA_HOST_OS="$lima_host_os" \
  WM_SMOKE_LIMA_HOST_ARCH="$alternate_lima_host_arch" \
  WM_SMOKE_LIMA_VM_TYPE="$lima_vm_type" \
  WM_SMOKE_LIMA_HOME="$lima_home" \
  WM_SMOKE_QEMU_LOG="$qemu_log" \
  sh "$repo_root/install.sh" > "$smoke_root/split-arch-output.log"
cmp "$asset_dir/$native_asset" "$split_install_dir/watermelon"
cmp "$asset_dir/$alternate_sidecar" "$split_install_dir/$alternate_sidecar"
grep -Fqx "file://$asset_dir/$native_asset" "$curl_log"
grep -Fqx "file://$asset_dir/$alternate_sidecar" "$curl_log"
if [ "$lima_host_os" = linux ]; then
  grep -Fqx "$alternate_qemu_name --version" "$qemu_log"
fi

version_output=$("$installed_cli" --version)
if [ "$version_output" != "watermelon version $expected_version" ]; then
  echo "installed release version = $version_output, want watermelon version $expected_version" >&2
  exit 1
fi
if [ "${run_sidecar:-false}" = true ]; then
  "$installed_sidecar" -h >/dev/null 2>&1
fi

init_project="$smoke_root/init-project"
mkdir -p "$init_project"
(
  cd "$init_project"
  "$installed_cli" init >/dev/null
)
if ! grep -Fqx 'enforcement = "fail"' "$init_project/.watermelon.toml"; then
  echo "installed release did not initialize strict enforcement" >&2
  exit 1
fi

echo "release installer smoke tests passed on $(uname -s)/$(uname -m)"
