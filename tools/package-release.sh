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
for required in go tar shasum python3; do
  command -v "$required" >/dev/null 2>&1 || {
    echo "Required build command is missing: $required" >&2
    exit 1
  }
done

repo_dir=$(dirname "$(realpath "$0")")/..
if [ -n "$(git -C "$repo_dir" status --porcelain)" ]; then
  echo "Release packaging requires a clean committed source tree." >&2
  exit 1
fi
staging_dir=$(mktemp -d "${TMPDIR:-/tmp}/starry-cli-package.XXXXXX")
trap 'rm -rf "$staging_dir"' EXIT
trap 'exit 1' HUP INT TERM

python3 "$repo_dir/tools/sync-skill-references.py" --check
python3 "$repo_dir/tools/release-manifest.py" "$staging_dir/release-manifest.json"
build_flags=$(python3 -c 'import json,sys; m=json.load(open(sys.argv[1])); v={"Version":m["version"],"ReleaseID":m["releaseId"],"SourceCommit":m["source"]["baseCommit"],"SourceDigest":m["source"]["sourceDigest"],"SourceDirty":str(m["source"]["dirty"]).lower(),"BuildTime":m["builtAt"]}; print("-s -w "+" ".join("-X github.com/allenvissong-source/starry-cli/internal/cli."+k+"="+v for k,v in v.items()))' "$staging_dir/release-manifest.json")

for target_os in darwin linux windows; do
  for target_arch in amd64 arm64; do
    name="starry-cli-$target_os-$target_arch"
    package_dir="$staging_dir/$name"
    mkdir "$package_dir"
    binary=starry-cli
    if [ "$target_os" = windows ]; then binary=starry-cli.exe; fi
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" GOWORK=off \
      GOPROXY=off GOTOOLCHAIN=local go -C "$repo_dir" build -trimpath \
      -ldflags="$build_flags" -o "$package_dir/$binary" ./cmd/starry-cli
    host_name=starry-format-host
    if [ "$target_os" = windows ]; then host_name=starry-format-host.exe; fi
    host=""
    if [ -n "${STARRY_FORMAT_HOST_DIR:-}" ] &&
      [ -f "$STARRY_FORMAT_HOST_DIR/$target_os-$target_arch/$host_name" ]; then
      host="$STARRY_FORMAT_HOST_DIR/$target_os-$target_arch/$host_name"
    fi
    if [ -n "$host" ]; then
      if [ ! -s "$host.NOTICES" ]; then
        echo "Format host license notices are missing: $host.NOTICES" >&2
        exit 1
      fi
      cp "$host" "$package_dir/$host_name"
      cp "$host.NOTICES" "$package_dir/FORMAT_HOST_NOTICES"
      chmod 755 "$package_dir/$host_name"
      printf '{"fileImportExport":true,"imagePreparation":true,"mediaUpload":true,"mediaDownload":true,"host":"%s"}\n' "$host_name" > "$package_dir/file-capabilities.json"
    else
      printf '{"fileImportExport":false,"imagePreparation":false,"mediaUpload":false,"mediaDownload":true,"reason":"format_host_not_built_for_target"}\n' > "$package_dir/file-capabilities.json"
    fi
    for skill in starry-community starry-character starry-worldbook starry-assets; do
      cp -R "$repo_dir/skills/$skill" "$package_dir/$skill"
    done
    find "$package_dir" -type f \( -name '.DS_Store' -o -name '._*' \) -delete
    cp "$repo_dir/INSTALL.md" "$package_dir/README.md"
    cp "$repo_dir/LICENSE" "$package_dir/LICENSE"
    cp "$repo_dir/THIRD_PARTY_NOTICES" "$package_dir/THIRD_PARTY_NOTICES"
    cp "$repo_dir/FILES.md" "$package_dir/FILES.md"
    cp "$staging_dir/release-manifest.json" "$package_dir/release-manifest.json"
    COPYFILE_DISABLE=1 tar --format=ustar -czf "$staging_dir/$name.tar.gz" -C "$staging_dir" "$name"
    checksum=$(shasum -a 256 "$staging_dir/$name.tar.gz")
    printf '%s  %s.tar.gz\n' "${checksum%% *}" "$name" >> "$staging_dir/SHA256SUMS"
    echo "Built $name"
  done
done
python3 "$repo_dir/tools/package-skills.py" "$staging_dir/starry-skills-v1.zip"
checksum=$(shasum -a 256 "$staging_dir/starry-skills-v1.zip")
printf '%s  starry-skills-v1.zip\n' "${checksum%% *}" >> "$staging_dir/SHA256SUMS"
cp "$staging_dir"/*.tar.gz "$staging_dir/starry-skills-v1.zip" "$staging_dir/release-manifest.json" "$staging_dir/SHA256SUMS" "$output_dir/"
echo "Packages written to $output_dir"
