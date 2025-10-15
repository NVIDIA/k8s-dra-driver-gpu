**Warning:** the test suite runs _invasively_ against the Kubernetes cluster that your local `kubectl` is currently configured against.

## Usage

Review the `TEST_*` variables [at around the top of the Makefile](https://github.com/NVIDIA/k8s-dra-driver-gpu/blob/main/tests/bats/Makefile#L22). Most of them can be overridden via environment.
Use this configuration interface to customize your test run.

Then invoke `make bats` in the root of the repository.

Some examples are shown below.

### Test a specific GHCR chart version

Example:

```console
$ export TEST_CHART_VERSION="25.8.0-dev-b823882b-chart"
$ make bats
...
12 tests, 0 failures in 166 seconds
```

Note: by default, the test suite assumes availability of a Helm chart on `oci://ghcr.io/nvidia/k8s-dra-driver-gpu`, pointing to a container image also publicly available in that registry.


### Test local dev state (artifacts not pushed)

To test the Helm chart currently specified in `deployments/helm/nvidia-dra-driver-gpu` in the local checkout, run

```console
TEST_CHART_LOCAL=1 make bats
```

This overrides `TEST_CHART_REPO` and `TEST_CHART_VERSION`.

Make sure (out-of-band) that the container images that the local chart refers to are available to all nodes in the Kubernetes cluster -- placed directly (TODO: how-to) or pullable.

### Defaults

By default, `make bats` tries to install a Helm chart from  `oci://ghcr.io/nvidia/k8s-dra-driver-gpu` corresponding to the git revision of the local checkout:

```console
$ git rev-parse --short=8 HEAD
e6e1dde4
$ make bats
...
        --env TEST_CHART_REPO="oci://ghcr.io/nvidia/k8s-dra-driver-gpu" \
        --env TEST_CHART_VERSION=25.8.0-dev-e6e1dde4-chart \
        --env TEST_CRD_UPGRADE_TARGET_GIT_REF=e6e1dde4 \
...
```

That's CI-oriented.
We may want to change that.


## Development

Bats is a workable solution.
Developing new tests might however probe your patience.
Make wise usage of

* bats' [`run`](https://bats-core.readthedocs.io/en/stable/writing-tests.html#run-test-other-commands) command.
* [skipping tests](https://bats-core.readthedocs.io/en/stable/writing-tests.html#skip-easily-skip-tests)
* [tagging tests with `bats:focus`](https://bats-core.readthedocs.io/en/stable/writing-tests.html#special-tags)
* [CLI args](https://bats-core.readthedocs.io/en/stable/usage.html) such as `--verbose-run`, `--show-output-of-passing-tests`.

Misc notes:

* The test suite stops on first failure (using the [new](https://github.com/bats-core/bats-core/issues/209) `--abort` flag for bats).
  The tests are not perfectly independent yet, and hence that's sane default behavior.
* Don't skip the section about when [not to use `run`](https://bats-core.readthedocs.io/en/stable/writing-tests.html#when-not-to-use-run).
* Take inspiration from [cri-o tests](https://github.com/cri-o/cri-o/tree/81e69a58c7e6ec8699b3bdd8696b1d0e25e32bfb/test).
* We can and should radically iterate on the test suite's config interface to satisfy our needs.