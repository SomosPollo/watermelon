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
script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
repo_root=$(CDPATH='' cd "$script_dir/.." && pwd -P)

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
    ;;
  Darwin:x86_64)
    native_asset=watermelon-darwin-amd64
    native_sidecar=watermelon-nfqd-linux-amd64
    ;;
  Linux:aarch64|Linux:arm64)
    native_asset=watermelon-linux-arm64
    native_sidecar=watermelon-nfqd-linux-arm64
    run_sidecar=true
    ;;
  Linux:x86_64)
    native_asset=watermelon-linux-amd64
    native_sidecar=watermelon-nfqd-linux-amd64
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
install_dir="$smoke_root/install"
installer_tmp="$smoke_root/tmp"
installer_home="$smoke_root/home"
curl_log="$smoke_root/curl.log"
mkdir -p "$fake_bin" "$install_dir" "$installer_tmp" "$installer_home"
: > "$curl_log"

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

cat > "$fake_bin/sudo" <<'EOF'
#!/bin/sh
echo "installer unexpectedly invoked sudo: $*" >&2
exit 1
EOF

chmod +x "$fake_bin/curl" "$fake_bin/sudo"

PATH="$fake_bin:$PATH" \
  HOME="$installer_home" \
  TMPDIR="$installer_tmp" \
  WATERMELON_INSTALL_DIR="$install_dir" \
  WM_SMOKE_ASSET_DIR="$asset_dir" \
  WM_SMOKE_CURL_LOG="$curl_log" \
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
