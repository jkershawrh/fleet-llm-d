# llm-d upstream contribution package

This directory contains draft material for starting an upstream discussion
with llm-d SIG Router. It is deliberately narrower than the complete
fleet-llm-d implementation.

The proposed sequence is:

1. Review and post `rfc-discussion.md` as a GitHub discussion or RFC issue.
2. Present the problem and experience report in `#sig-router`.
3. Ask maintainers whether the work belongs in `llm-d-router`, a new
   `llm-d-incubation` repository, or a multi-cluster Well-Lit Path.
4. Revise `proposal-fleet-level-multicluster-orchestration.md` based on that
   feedback and submit it under `llm-d/llm-d/proposals/`.
5. Submit implementation and test changes as small DCO-signed pull requests.

Do not submit the historical `docs/proposals/fleet-orchestration.md` directly.
It describes superseded components and transport choices and is much broader
than the initial upstream discussion should be.
