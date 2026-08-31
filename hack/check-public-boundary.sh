#!/usr/bin/env bash
set -euo pipefail

failed=0

forbidden_paths='^(deploy/(oberon|arena|brutus|certification)/|deploy/kustomize/overlays/(oberon|oberon-router-beta|arena|brutus)/|docs/operations/|test/benchmarks/reports/)'
if git ls-files | grep -E "$forbidden_paths"; then
  echo "ERROR: environment-owned deployment or evidence files are tracked"
  failed=1
fi

internal_refs='fm2aihpcsed|192\.168\.1\.|oberon|arena|brutus'
if git grep -n -I -E "$internal_refs" -- \
  . \
  ':!hack/check-public-boundary.sh' \
  ':!hack/verify-oss-release.sh' \
  ':!.pre-commit-config.yaml' \
  ':!.gitignore'; then
  echo "ERROR: internal infrastructure references are tracked"
  failed=1
fi

exit "$failed"
