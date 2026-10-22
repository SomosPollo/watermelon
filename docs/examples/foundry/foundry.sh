#!/bin/sh
set -eu

if [ "$#" -eq 0 ]; then
    echo "Usage: sh foundry.sh <forge|cast|anvil|chisel> [args...]" >&2
    exit 2
fi
foundry_tool=$1
shift
case "$foundry_tool" in
    forge|cast|anvil|chisel) ;;
    *) echo "Unsupported Foundry command: $foundry_tool" >&2; exit 2 ;;
esac

set -- ghcr.io/foundry-rs/foundry:latest "$@"
if [ -t 0 ]; then
    set -- -t "$@"
fi

exec nerdctl run --rm -i --network=host \
    --entrypoint "$foundry_tool" \
    -v "$PWD:$PWD" -w "$PWD" "$@"
