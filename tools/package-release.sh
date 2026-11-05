#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "Usage: sh tools/package-release.sh /absolute/existing/empty/output" >&2
  exit 1
fi
output_dir=$1
case "$output_dir" in
  /*) ;;
  *) echo "Output directory must be absolute." >&2; exit 1 ;;
esac
if [ ! -d "$output_dir" ]; then
  echo "Output directory must already exist." >&2
  exit 1
fi
for existing in "$output_dir"/* "$output_dir"/.[!.]* "$output_dir"/..?*; do
  if [ -e "$existing" ] || [ -L "$existing" ]; then
    echo "Output directory must be empty." >&2
    exit 1
  fi
done
for required in go tar shasum; do
  command -v "$required" >/dev/null 2>&1 || {
    echo "Required build command is missing: $required" >&2
    exit 1
  }
done

repo_dir=$(dirname "$(realpath "$0")")/..
staging_dir=$(mktemp -d "${TMPDIR:-/tmp}/starry-cli-package.XXXXXX")
trap 'rm -rf "$staging_dir"' EXIT
trap 'exit 1' HUP INT TERM

native_os=$(go env GOHOSTOS)
native_arch=$(go env GOHOSTARCH)
native_host="$staging_dir/starry-format-host"
if command -v dart >/dev/null 2>&1; then
  sh "$repo_dir/tools/build-format-host.sh" "$native_host"
fi

for target_os in darwin linux windows; do
  for target_arch in amd64 arm64; do
    name="starry-cli-$target_os-$target_arch"
    package_dir="$staging_dir/$name"
    mkdir "$package_dir"
    binary=starry-cli
    if [ "$target_os" = windows ]; then binary=starry-cli.exe; fi
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" GOWORK=off \
      GOPROXY=off GOTOOLCHAIN=local go -C "$repo_dir" build -trimpath \
      -ldflags="-s -w" -o "$package_dir/$binary" ./cmd/starry-cli
    host_name=starry-format-host
    if [ "$target_os" = windows ]; then host_name=starry-format-host.exe; fi
    host=""
    if [ -n "${STARRY_FORMAT_HOST_DIR:-}" ] &&
      [ -f "$STARRY_FORMAT_HOST_DIR/$target_os-$target_arch/$host_name" ]; then
      host="$STARRY_FORMAT_HOST_DIR/$target_os-$target_arch/$host_name"
    elif [ "$target_os" = "$native_os" ] && [ "$target_arch" = "$native_arch" ] && [ -f "$native_host" ]; then
      host="$native_host"
    fi
    if [ -n "$host" ]; then
      cp "$host" "$package_dir/$host_name"
      chmod 755 "$package_dir/$host_name"
      printf '{"fileImportExport":true,"host":"%s"}\n' "$host_name" > "$package_dir/file-capabilities.json"
    else
      printf '{"fileImportExport":false,"reason":"format_host_not_built_for_target"}\n' > "$package_dir/file-capabilities.json"
    fi
    for skill in starry-community starry-character starry-worldbook; do
      cp -R "$repo_dir/skills/$skill" "$package_dir/$skill"
    done
    find "$package_dir" -type f \( -name '.DS_Store' -o -name '._*' \) -delete
    cp "$repo_dir/INSTALL.md" "$package_dir/README.md"
    cp "$repo_dir/FILES.md" "$package_dir/FILES.md"
    COPYFILE_DISABLE=1 tar -czf "$staging_dir/$name.tar.gz" -C "$staging_dir" "$name"
    checksum=$(shasum -a 256 "$staging_dir/$name.tar.gz")
    printf '%s  %s.tar.gz\n' "${checksum%% *}" "$name" >> "$staging_dir/SHA256SUMS"
    echo "Built $name"
  done
done
cp "$staging_dir"/*.tar.gz "$staging_dir/SHA256SUMS" "$output_dir/"
echo "Packages written to $output_dir"
