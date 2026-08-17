#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  echo "usage: $0 TAG CHANGELOG" >&2
  exit 2
fi

tag=$1
changelog=$2

case "$tag" in
  v?*) ;;
  *)
    echo "release tag must start with v: $tag" >&2
    exit 2
    ;;
esac

if [ ! -s "$changelog" ]; then
  echo "changelog is missing or empty: $changelog" >&2
  exit 1
fi

version=${tag#v}
target="## [$version]"

awk -v target="$target" '
  function is_version_heading(line) {
    return index(line, "## [") == 1
  }

  function is_target_heading(line) {
    return index(line, target) == 1 && \
      (length(line) == length(target) || substr(line, length(target) + 1, 1) == " ")
  }

  is_version_heading($0) {
    emit = is_target_heading($0)
    if (emit) {
      found++
      started = 0
    }
    next
  }

  emit {
    if (!started && $0 == "") {
      next
    }
    print
    started = 1
    if ($0 != "") {
      content++
    }
  }

  END {
    if (found != 1) {
      printf "expected exactly one changelog section for %s, found %d\n", target, found > "/dev/stderr"
      exit 1
    }
    if (content == 0) {
      printf "changelog section for %s is empty\n", target > "/dev/stderr"
      exit 1
    }
  }
' "$changelog"
