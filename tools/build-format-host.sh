#!/bin/sh
set -eu
if [ "$#" -ne 1 ] || [ ! -d "$(dirname "$1")" ]; then
  echo "Usage: sh tools/build-format-host.sh /absolute/existing/output/starry-format-host" >&2
  exit 1
fi
case "$1" in /*) ;; *) echo "Output must be absolute" >&2; exit 1 ;; esac
repo_dir=$(dirname "$(realpath "$0")")/..
packages="$repo_dir/../starry_character_card_codec/.dart_tool/package_config.json"
if [ ! -f "$packages" ]; then
  echo "Resolve the existing sibling codec packages before building" >&2
  exit 1
fi
dart compile exe --packages="$packages" "$repo_dir/tools/format-host.dart" -o "$1"
