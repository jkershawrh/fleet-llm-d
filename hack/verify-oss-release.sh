#!/usr/bin/env bash
set -euo pipefail

project_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
archive=${1:-}

if [[ -z "$archive" ]]; then
  archive=$("$project_root/hack/build-oss-release.sh" test)
fi
if [[ ! -f "$archive" ]]; then
  echo "release archive not found: $archive" >&2
  exit 1
fi

extract_root=$(mktemp -d "${TMPDIR:-/tmp}/fleet-llm-d-verify.XXXXXX")
trap 'rm -rf "$extract_root"' EXIT
tar -xzf "$archive" -C "$extract_root"
bundle_root=$(find "$extract_root" -mindepth 1 -maxdepth 1 -type d -name 'fleet-llm-d-*' -print -quit)

required=(
  README.md LICENSE NOTICE OSS-CONTENTS.txt VERSION
  go.mod Cargo.toml api/crds charts/fleet-llm-d
  cmd/fleet-controller cmd/grid-signals-publisher crates/fleet-agent pkg
  deploy/kustomize/overlays/community
  deploy/kustomize/components/llmd-router-endpoints
  docs/community/deployment-profiles.md
)
for path in "${required[@]}"; do
  if [[ ! -e "$bundle_root/$path" ]]; then
    echo "portable release is missing required path: $path" >&2
    exit 1
  fi
done

banned_paths='/(oberon|arena|brutus|environments|secrets\.yaml)(/|$)'
if tar -tzf "$archive" | grep -Eiq "$banned_paths"; then
  echo "portable release contains an environment-specific path" >&2
  tar -tzf "$archive" | grep -Ei "$banned_paths" >&2
  exit 1
fi

banned_content='fm2aihpcsed|image-registry\.openshift-image-registry|quay\.io/rh-ee-|/Users/jkershaw|kubeadmin|oberon|arena|brutus|192\.168\.1\.'
if grep -RIEin --exclude='verify-oss-release.sh' "$banned_content" "$bundle_root"; then
  echo "portable release contains environment-specific content" >&2
  exit 1
fi

banned_deployment_content='registry\.redhat\.io|hostPath:'
deployment_paths=("$bundle_root/deploy" "$bundle_root/charts" "$bundle_root/docs" "$bundle_root/README.md")
if grep -RIEin "$banned_deployment_content" "${deployment_paths[@]}"; then
  echo "portable release contains a non-portable deployment dependency" >&2
  exit 1
fi

if grep -RIEin 'ghcr\.io/OWNER/fleet-llm-d|newTag: VERSION|tag=VERSION' \
  "$bundle_root/deploy" "$bundle_root/docs"; then
  echo "portable release contains unstamped image placeholders" >&2
  exit 1
fi

if find "$bundle_root" -type l -print -quit | grep -q .; then
  echo "portable release contains symbolic links" >&2
  exit 1
fi

echo "verified portable OSS release: $archive"
