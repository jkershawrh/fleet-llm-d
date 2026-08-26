#!/usr/bin/env bash
set -euo pipefail

project_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
version=${1:-${VERSION:-dev}}
output_dir=${2:-"$project_root/dist"}
manifest="$project_root/release/oss-include.txt"
image_prefix=${OSS_IMAGE_PREFIX:-${GITHUB_REPOSITORY:-jkershawrh/fleet-llm-d}}

if [[ ! "$version" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]*$ ]]; then
  echo "invalid release version: $version" >&2
  exit 1
fi
if [[ ! "$image_prefix" =~ ^[a-z0-9._-]+/[a-z0-9._/-]+$ ]]; then
  echo "invalid OSS image prefix: $image_prefix" >&2
  exit 1
fi

mkdir -p "$output_dir"
stage_root=$(mktemp -d "${TMPDIR:-/tmp}/fleet-llm-d-oss.XXXXXX")
trap 'rm -rf "$stage_root"' EXIT
bundle_root="$stage_root/fleet-llm-d-$version"
mkdir -p "$bundle_root"

includes=()
while IFS= read -r entry; do
  [[ -z "$entry" || "$entry" == \#* ]] && continue
  if [[ ! -e "$project_root/$entry" ]]; then
    echo "OSS release manifest references missing path: $entry" >&2
    exit 1
  fi
  includes+=("$entry")
done < "$manifest"

(
  cd "$project_root"
  COPYFILE_DISABLE=1 tar \
    --exclude='node_modules' \
    --exclude='.next' \
    --exclude='target' \
    --exclude='bin' \
    --exclude='dist' \
    -cf - "${includes[@]}"
) | tar -xf - -C "$bundle_root"

cp "$project_root/release/COMMUNITY-README.md" "$bundle_root/README.md"
cp "$manifest" "$bundle_root/OSS-CONTENTS.txt"
printf '%s\n' "$version" > "$bundle_root/VERSION"

render_template() {
  local path=$1
  local rendered="$path.rendered"
  sed \
    -e "s|ghcr.io/OWNER/fleet-llm-d|ghcr.io/$image_prefix|g" \
    -e "s|newTag: VERSION|newTag: $version|g" \
    -e "s|tag=VERSION|tag=$version|g" \
    "$path" > "$rendered"
  mv "$rendered" "$path"
}

render_template "$bundle_root/deploy/kustomize/overlays/community/kustomization.yaml"
render_template "$bundle_root/docs/community/installation.md"

archive="$output_dir/fleet-llm-d-$version-oss-source.tar.gz"
COPYFILE_DISABLE=1 tar -czf "$archive" -C "$stage_root" "fleet-llm-d-$version"
echo "$archive"
