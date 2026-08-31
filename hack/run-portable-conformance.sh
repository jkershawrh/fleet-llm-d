#!/usr/bin/env bash
set -euo pipefail

project_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project_root"

echo "==> fleet eligibility and routing policy"
go test -count=1 ./pkg/routing/... ./pkg/placement/... ./pkg/tenant/...

echo "==> inference gateway and exact-model behavior"
go test -count=1 ./pkg/server

echo "==> portable API and adapter contracts"
go test -count=1 -tags=contracts ./test/contracts/...
go test -count=1 -tags=architecture ./test/architecture/...
go test -count=1 -tags=security ./test/security/...

echo "==> agent-side admission and quota enforcement"
cargo test --locked --workspace

echo "==> portable source boundary"
bash hack/check-public-boundary.sh
version=${CONFORMANCE_VERSION:-conformance}
archive=$(bash hack/build-oss-release.sh "$version")
bash hack/verify-oss-release.sh "$archive"

echo "portable conformance passed"
