---
title: NNNN — Template
linkTitle: Template
weight: 1
description: Copy this when writing a new proposal.
---

<!--
Before writing this, pause and check:

- Does this change really belong in this driver? A common outcome for
  proposals here is "wrong layer" — the change really belonged in upstream
  Kubernetes, in the device plugin, or in the GPU operator. If you're not
  sure, open a discussion issue first and link it here.
- If you can't yet write a one-paragraph release note for this change, you
  aren't ready to propose it.
- Small changes (a flag, a config knob, a local refactor) go straight to
  a PR. See docs/proposals/README.md for when a proposal is actually
  needed.
-->

| Field          | Value         |
|----------------|---------------|
| Status         | provisional   |
| Authors        | @your-handle  |
| Created        | YYYY-MM-DD    |
| Related issues | #…            |

## Summary

<!--
One paragraph. Should read like the release note users will see on merge —
write this first. If you can't, you don't understand the change yet.
-->

## Motivation

### Who is asking for this, and why?

<!--
Name a concrete workload, a named downstream consumer (for example
KubeVirt, LWS, JobSet, Slurm, DCGM-Exporter), or a specific user class.
Abstract motivation without a named use case is a common reason proposals
stall.
-->

### Goals

<!--
Each goal should be measurable enough to imply a test. "How will we know
this has succeeded?" is the question reviewers will ask.
-->

### Non-goals

<!--
Explicit fence. What are you NOT proposing, even if related? This is where
you prevent scope creep during review.
-->

## Why this belongs in the NVIDIA DRA driver

<!--
Required. State why this change lives here and not in:
- upstream Kubernetes / DRA core
- the device plugin
- the GPU operator
- a CLI or external tool
If any part of this requires an upstream Kubernetes change, link the
blocking issue or discussion.
-->

## Proposal

### User-facing example

<!--
Required. Show the concrete surface users will touch: a ResourceClaim
manifest, a CRD sample, a CLI invocation, a Helm values snippet. If this
feels verbose or repetitive, that's a signal the UX needs rework before
the internals do.
-->

```yaml
# example
```

### Affected components

<!-- Check all that apply. Matches the components dropdown on the feature request issue form. -->

- [ ] `api/` — CRDs, CRD fields, or ResourceClaim shape
- [ ] `gpu-kubelet-plugin`
- [ ] `compute-domain-kubelet-plugin`
- [ ] `compute-domain-controller`
- [ ] `compute-domain-daemon`
- [ ] admission webhook
- [ ] Helm chart (`deployments/helm`)
- [ ] CDI spec generation
- [ ] Metrics
- [ ] Kubelet-plugin checkpoint schema
- [ ] Documentation
- [ ] CI / testing

### Authoritative state owner

<!--
For any new persistent or runtime state: which single component is
authoritative for writes? How do the others read it? Reviewers will push
back on state split across plugin + controller + daemon without a clear
owner.

If you haven't worked in this codebase before, the top-level README and
the directories under `cmd/` describe what each component does.
-->

### Smallest valuable slice

<!--
What is the smallest piece that could ship first and still be useful?
Large PRs routinely get asked to split — plan the decomposition now.
-->

## Design

### API changes

<!--
List new or changed CRD fields, flags, annotations, Helm values. For each
new field, explicitly label it "user-facing" or "implementation detail."
Default to NOT exposing speculative fields — once users depend on them,
removal is painful.
-->

### Feature gate & graduation

<!--
Does this introduce a feature gate? If yes:
- Target stability at ship time (Alpha / Beta / Stable).
- If not Stable, what does graduation to the next level require? Use the
  project's three criteria (see docs/proposals/README.md): feature
  completeness, interoperability (name the adjacent features), stability
  and soak time.
- Which existing feature gates is this mutually exclusive with, or does it
  compose with?
If no gate, state what the change is guarded by (flag, config, chart
value) or "none — ships on by default."
-->

### Upgrade & downgrade

<!--
What happens if a cluster upgrades (or rolls back) the driver while claims
are in flight?
- If the kubelet-plugin checkpoint schema changes: use `omitempty` on new
  fields and describe the upgrade test.
- Helm value migrations, CRD conversion, RBAC changes.
- Rolling restart of controller replicas — does anything get lost?
-->

### Environment floor

<!--
- Minimum NVIDIA driver version.
- GPU generations supported (A100 / H100 / B200 / GB200 / L40 / …).
- MIG / NVLink / IMEX / fabric requirements.
- Minimum Kubernetes version.
- Required or assumed kubelet/controller feature gates.
-->

### Test plan

<!--
Reviewers will not approve without a concrete plan across more than unit
tests. Name the specific files/jobs where you can.
- Unit: …
- Integration / controller tests: …
- BATS (`tests/bats`): …
- Mock NVML CI (`hack/ci/mock-nvml`): can this be exercised on CPU-only
  runners? If no, why not?
- Lambda e2e on real GPUs: which `pull-dra-driver-nvidia-gpu-*` job, and
  which scenario (MIG / time-slicing / ComputeDomain / …)?
-->

## Risks

<!--
Think broadly. Concerns reviewers consistently raise here:

- Races between kubelet prepare/unprepare and controller/daemon state.
- Force-deleted pods, node reboots, or daemon restarts mid-claim.
- Multi-node coordination for ComputeDomains (SSA mutation cache, DNS
  index collisions, leader election under partition).
- Privilege surface: privileged containers, /dev mounts, NVML access,
  nvidia-container-runtime interactions.
- Fail-fast preservation: does the process still exit non-zero on
  well-defined fatal conditions after this change?
-->

## Alternatives

<!--
What did you rule out, and why? Include at minimum:

- What you tried with existing primitives (CEL selectors, `matchAttribute`,
  current CRD knobs, Helm values) and why they were insufficient.
- Adjacent prior art in NVIDIA repos — go-nvlib, dra-example-driver,
  sandbox-device-plugin, nvml-mock — and whether any of it can be reused.
- Relevant upstream Kubernetes work, even if only to state "considered,
  not applicable because …".
-->

## Drawbacks

<!--
Steelman the case against this change. If you can't find one, the proposal
isn't ready.
-->

## Open questions

<!--
Anything you want explicit maintainer input on before implementation.
These are the conversations worth having here rather than in code review.
-->