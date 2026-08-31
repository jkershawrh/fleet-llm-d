# Community release boundary

The portable OSS artifact is an allowlisted projection of the full source
repository. Building it never deletes, moves, or edits environment material.
The definitive include list is `release/oss-include.txt`.

The artifact is the complete portable source distribution. Production scale
and HA are supported deployment profiles of this same OSS codebase; they are
not a closed or separate "scale edition." Governed-evidence integrations are
optional composition, while physical deployment overlays and raw evidence stay
outside the portable artifact.

## Included

- Apache-2.0 controller (including OpenAI-compatible ingress), agent, CLI, API,
  and dashboard source.
- Generic Helm and Kustomize deployment resources.
- Generic Praxis and llm-d Router adapter contracts and endpoint-discovery
  resources.
- Community PostgreSQL evaluation component.
- Mock inference backend and portable automated tests.
- Observability dashboards and alert definitions.
- License, notice, version, and source-content manifest.

## Preserved in the repository but excluded from the artifact

- Physical-cluster manifests and scripts.
- Internal registry image digests and platform-specific credentials.
- Soak jobs, benchmark reports, historical evidence, and architecture working
  documents.
- Customer-pattern, presentation, whitepaper, and ecosystem integration
  material.
- Optional external services and locally built demonstration images.
- Model artifacts and semantic-classifier server binaries.

These exclusions are packaging boundaries, not deletions. Maintainers continue
to use the full repository for development and fleet operations. Public release
consumers receive only the portable artifact and signed public images.

## Build and verify

```sh
make oss-release VERSION=0.3.0
make verify-oss-release
make test-portable-conformance
```

From an extracted source bundle, run the same gate directly:

```sh
bash hack/run-portable-conformance.sh
```

`test-portable-conformance` verifies fleet eligibility, policy precedence,
exact-model gateway behavior, adapter/API contracts, agent-side admission, and
the source-bundle boundary. It does not require or name a physical cluster.
Environment certification and load capacity remain separate activities.

Release CI uses the GitHub repository name as the GHCR image prefix and stamps
the release version into the generated community Kustomize and installation
documentation. Local builds default to the current public project namespace;
set `OSS_IMAGE_PREFIX=owner/repository` to test a fork.

Tagged releases sign container-image digests, attach CycloneDX SBOMs, and issue
Sigstore-backed GitHub build-provenance attestations for images, binaries, the
portable source archive, checksums, and SBOM files. Tags are convenience names;
verification and production pinning must use the attested digest.
