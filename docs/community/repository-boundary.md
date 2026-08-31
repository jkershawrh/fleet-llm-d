# OSS and downstream production boundary

This repository contains the portable fleet control plane, generic deployment
profiles, adapters, conformance tests, and reproducible test harnesses.

Environment-owned material must be maintained in a separate private
repository. That includes:

- cluster names, addresses, Routes, registries, and kubeconfigs;
- credentials, trust bundles, and Secret values;
- physical-cluster Kustomize overlays and rollout scripts;
- certification Jobs tied to a particular topology;
- raw benchmark, soak, telemetry, and incident evidence.

The private downstream repository may consume a tagged OSS release and layer
its configuration on top. It must not be stored on another branch of this
public repository because branches and Git history remain publicly readable.

`hack/check-public-boundary.sh` rejects known environment paths and internal
infrastructure references. It runs in pre-commit, CI, release workflows, and
portable conformance. `.gitignore` provides a second guard for common local
files, but it does not replace secret scanning or remove previously tracked
content from Git history.
