<!--  Thanks for sending a pull request! See CONTRIBUTING.md for guidelines. -->

#### What type of PR is this?

<!--
Add one of the following kinds:
/kind bug
/kind cleanup
/kind dependency
/kind documentation
/kind feature
-->

#### What this PR does / why we need it:

#### Which issue(s) this PR is related to:
<!--
Use "Fixes #<issue number>" to auto-close the issue on merge.
Write "N/A" if there is no associated issue.
-->

#### Special notes for your reviewer:

#### Does this PR introduce a user-facing change?
<!--
Write "NONE" if there is no user-facing change.
Otherwise, describe the change for driver users / cluster admins. Examples:
- new or changed CLI flags on the kubelet-plugin / controller
- changes to DeviceClass, ResourceClaim, or CRD shapes
- behavior changes for MIG, time-slicing, IMEX, or sharing modes
- deployment/Helm chart changes (values, defaults, RBAC)
Include "action required" if users must change configs or manifests when upgrading.
-->
```release-note

```

#### Additional documentation (design docs, usage docs, etc.):

<!--
Link any supporting docs, e.g.:
- updates under docs/ (design, MIG, IMEX, sharing, etc.)
- README or demo/ updates
- related KEPs in kubernetes/enhancements
Use permalinks (commit SHA) rather than branch links so references stay stable.
-->
```docs

```

#### Checklist

<!-- Tick what applies; leave others unchecked. CI re-runs some of these, but catching them locally shortens the review loop. -->
- [ ] `make check test` passes locally
- [ ] `make check-generate` passes if `api/` changed (CRDs, deepcopy, informers, listers, clientset)
- [ ] `make check-modules` passes if `go.mod` / `go.sum` changed
- [ ] Tests added or updated for the change
- [ ] Helm chart (`helm/`) updated if flags, RBAC, or defaults changed
