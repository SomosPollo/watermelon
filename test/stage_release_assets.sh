#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  echo "usage: $0 GORELEASER_DIST_DIR OUTPUT_DIR" >&2
  exit 2
fi

command -v jq >/dev/null 2>&1 || {
  echo "jq is required to stage GoReleaser artifacts" >&2
  exit 1
}

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
repo_root=$(CDPATH='' cd "$script_dir/.." && pwd -P)
case "$1" in
  /*) dist_dir=$1 ;;
  *) dist_dir=$repo_root/$1 ;;
esac
case "$2" in
  /*) output_dir=$2 ;;
  *) output_dir=$repo_root/$2 ;;
esac

if [ ! -s "$dist_dir/artifacts.json" ] || [ ! -s "$dist_dir/checksums.txt" ]; then
  echo "GoReleaser metadata or checksums are missing from $dist_dir" >&2
  exit 1
fi
if [ -d "$output_dir" ] && [ -n "$(find "$output_dir" -mindepth 1 -print -quit)" ]; then
  echo "release staging directory is not empty: $output_dir" >&2
  exit 1
fi
mkdir -p "$output_dir"

records_file=$(mktemp "${TMPDIR:-/tmp}/watermelon-release-assets.XXXXXX")
cleanup() {
  rm -f -- "$records_file"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM

jq -er '.[] | select(.extra.Format == "binary") | [.name, .path] | @tsv' \
  "$dist_dir/artifacts.json" > "$records_file"

asset_count=$(wc -l < "$records_file" | tr -d '[:space:]')
if [ "$asset_count" -ne 6 ]; then
  echo "GoReleaser produced $asset_count binary assets; expected 6" >&2
  exit 1
fi

tab=$(printf '\t')
while IFS="$tab" read -r name artifact_path; do
  case "$name" in
    watermelon-darwin-amd64 | \
    watermelon-darwin-arm64 | \
    watermelon-linux-amd64 | \
    watermelon-linux-arm64 | \
    watermelon-nfqd-linux-amd64 | \
    watermelon-nfqd-linux-arm64) ;;
    *)
      echo "unexpected GoReleaser binary asset: $name" >&2
      exit 1
      ;;
  esac
  case "$artifact_path" in
    /*) source_path=$artifact_path ;;
    *) source_path=$repo_root/$artifact_path ;;
  esac
  if [ ! -s "$source_path" ] || [ -e "$output_dir/$name" ]; then
    echo "GoReleaser asset is missing, empty, or duplicated: $name" >&2
    exit 1
  fi
  cp "$source_path" "$output_dir/$name"
done < "$records_file"

cp "$dist_dir/checksums.txt" "$output_dir/checksums.txt"

for asset in \
  watermelon-darwin-amd64 \
  watermelon-darwin-arm64 \
  watermelon-linux-amd64 \
  watermelon-linux-arm64 \
  watermelon-nfqd-linux-amd64 \
  watermelon-nfqd-linux-arm64
do
  if [ ! -s "$output_dir/$asset" ] || [ ! -x "$output_dir/$asset" ]; then
    echo "staged release asset is missing, empty, or not executable: $asset" >&2
    exit 1
  fi
done

echo "staged 6 GoReleaser binaries and checksums"
