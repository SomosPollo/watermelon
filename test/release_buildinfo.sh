#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  echo "usage: $0 ASSET_DIR EXPECTED_VERSION" >&2
  exit 2
fi

asset_dir=$1
expected_version=$2
module=github.com/saeta-eth/watermelon

metadata_value() {
  key=$1
  awk -v key="$key" '$1 == "build" && index($2, key "=") == 1 { print substr($2, length(key) + 2) }'
}

validate_asset() {
  name=$1
  command_path=$2
  expected_os=$3
  expected_arch=$4
  require_static=$5
  path="$asset_dir/$name"

  if [ ! -s "$path" ]; then
    echo "release asset is missing or empty: $path" >&2
    exit 1
  fi
  metadata=$(go version -m "$path")
  actual_command=$(printf '%s\n' "$metadata" | awk '$1 == "path" { print $2 }')
  actual_module=$(printf '%s\n' "$metadata" | awk '$1 == "mod" { print $2 }')
  actual_version=$(printf '%s\n' "$metadata" | awk '$1 == "mod" { print $3 }')
  actual_os=$(printf '%s\n' "$metadata" | metadata_value GOOS)
  actual_arch=$(printf '%s\n' "$metadata" | metadata_value GOARCH)
  vcs_modified=$(printf '%s\n' "$metadata" | metadata_value vcs.modified)

  if [ "$actual_command" != "$command_path" ] ||
     [ "$actual_module" != "$module" ] ||
     [ "$actual_version" != "$expected_version" ] ||
     [ "$actual_os" != "$expected_os" ] ||
     [ "$actual_arch" != "$expected_arch" ] ||
     [ "$vcs_modified" != "false" ]; then
    echo "release asset has unexpected Go build identity: $name" >&2
    printf '%s\n' "$metadata" >&2
    exit 1
  fi
  if [ "$require_static" = true ]; then
    cgo_enabled=$(printf '%s\n' "$metadata" | metadata_value CGO_ENABLED)
    if [ "$cgo_enabled" != "0" ]; then
      echo "release sidecar is not a static CGO-disabled build: $name" >&2
      printf '%s\n' "$metadata" >&2
      exit 1
    fi
  fi
}

validate_asset watermelon-darwin-arm64 "$module/cmd/watermelon" darwin arm64 false
validate_asset watermelon-darwin-amd64 "$module/cmd/watermelon" darwin amd64 false
validate_asset watermelon-linux-arm64 "$module/cmd/watermelon" linux arm64 false
validate_asset watermelon-linux-amd64 "$module/cmd/watermelon" linux amd64 false
validate_asset watermelon-nfqd-linux-arm64 "$module/cmd/watermelon-nfqd" linux arm64 true
validate_asset watermelon-nfqd-linux-amd64 "$module/cmd/watermelon-nfqd" linux amd64 true

echo "release Go build metadata matches $expected_version"
