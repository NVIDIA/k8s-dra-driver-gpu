# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial Giant Swarm fork: vendir-based upstream sync from `NVIDIA/k8s-dra-driver-gpu`.
- `sync/patches/team-label`: injects `application.giantswarm.io/team` into the chart's common labels.
- `sync/patches/network-policies`: explicit ingress deny + DNS egress on both upstream NetworkPolicies.
- `sync/patches/kyverno-policies`: Kyverno `PolicyException` template for the PSS-style policies that would otherwise block the driver's privileged + hostPath workloads (off by default; enable via `.Values.kyvernoPolicyExceptions.enabled`).
- Chart moved from `deployments/helm/dra-driver-nvidia-gpu/` to `helm/dra-driver-nvidia-gpu/` to match the Giant Swarm `template-app` layout.
- `.circleci/config.yml`, `renovate.json5`, `values.schema.json` for app-catalog publishing.
