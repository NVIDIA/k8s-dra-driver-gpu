# Enhancement proposals

This directory holds lightweight design proposals for the NVIDIA DRA driver.

A proposal is a short markdown document that answers a few high-signal
questions *before* code is written, so reviewers and authors are aligned
on scope, user-facing surface, component ownership, upgrade impact, and
test plan.

## When to file a proposal

**A proposal is required if the change:**

- Introduces a new feature gate, or graduates an existing gate to a new
  stability level.
- Makes a significant behavioral change to an existing feature.
- Adds or modifies a user-facing API: CRDs, CRD fields, CLI flags,
  Helm chart values, ResourceSlice attributes, or the kubelet-plugin
  checkpoint schema.
- Adds or modifies an external dependency (DCGM, NVSentinel, virtualization
  stack, etc.).
- Depends on a change in upstream Kubernetes, or requires coordination with
  work that is landing there.
- Touches the ComputeDomain / IMEX coordination layer in a way that affects
  multi-node behavior.

**A proposal is NOT required for:**

- Bug fixes to existing behavior.
- Documentation-only changes.
- Internal refactoring with no observable behavior change.
- Dependency version bumps with no API impact.
- Incremental additions under an existing feature gate that don't change
  the design.

If you're unsure, open an issue describing what you want to do and ask —
it's cheap to find out early.

## How to file one

1. Look at existing files in this directory and pick the next unused
   4-digit number (e.g., if `0001-foo.md` exists, name your file
   `0002-short-title.md`).
2. Copy `NNNN-template.md`, rename it, and fill it in. Start with
   `Status: provisional`.
3. Open a PR with the new document. You do not need to open an issue
   first, but feel free to if you want a quick gut-check on whether a
   proposal is needed.
4. Iterate on the PR with maintainers. A proposal is ready to merge when
   OWNERS from this repo approve — the same review flow as any code PR.
5. Once implementation lands, bump `Status` to `implemented` in a
   follow-up PR.

Proposals are typically a few hundred lines of markdown, not a novel.
Optimize for the reviewer's time: write the summary first, show a
concrete user-facing example, and don't dwell on sections that don't
apply to your change.

For questions or quick discussion, see the chat venues listed in
[CONTRIBUTING.md](../../CONTRIBUTING.md).

## Status lifecycle

- `provisional` — under discussion; design not yet agreed.
- `implementable` — design agreed; ready for implementation.
- `implemented` — shipped. Note the release in the document's header table.
- `withdrawn` — author or maintainers decided not to proceed. Leave the
  document in place as a decision record.

## Feature gate graduation

If your proposal introduces a feature gate, graduation to the next
stability level is evaluated against three criteria. Proposals that do
not involve a feature gate can skip this section.

- **Feature completeness** — the implementation aligns with the intended
  design goals.
- **Interoperability** — the feature works reliably alongside adjacent
  features (name the ones you tested against).
- **Stability and soak time** — the feature has been exercised in
  production-like environments for long enough to surface issues.

Constraints and caveats that accompany a feature gate should be captured
in release notes, not just in the proposal document.
