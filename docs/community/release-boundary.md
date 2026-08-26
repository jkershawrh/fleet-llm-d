# Community release boundary

The portable OSS artifact is an allowlisted projection of the full source
repository. Building it never deletes, moves, or edits environment material.
The definitive include list is `release/oss-include.txt`.

## Included

- Apache-2.0 controller (including OpenAI-compatible ingress), agent, CLI, API,
  and dashboard source.
- Generic Helm and Kustomize deployment resources.
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
```

Release CI uses the GitHub repository name as the GHCR image prefix and stamps
the release version into the generated community Kustomize and installation
documentation. Local builds default to the current public project namespace;
set `OSS_IMAGE_PREFIX=owner/repository` to test a fork.
