FROM debian:trixie

# GNU parallel: bats may want to use that
# gettext-base: provides envsubst, used by nickelpie
RUN apt-get update && apt-get install -y -q --no-install-recommends \
    parallel git ca-certificates curl make gettext-base jq && \
    rm -rf /var/lib/apt/lists/*

# Set by BuiltKit, of the form amd64/arm64.
ARG BUILDARCH

# Install bats for running cmdline tests.
# This is the image used when invoking `make bats-test`.
RUN git clone https://github.com/bats-core/bats-core.git && cd bats-core && \
    git checkout 658f442f0fcdd6f9e2ea01625999217e8f7bfe7d && ./install.sh /usr/local

RUN mkdir -p /bats-libraries
RUN git clone https://github.com/bats-core/bats-support /bats-libraries/bats-support
RUN git clone https://github.com/bats-core/bats-assert /bats-libraries/bats-assert
RUN git clone https://github.com/bats-core/bats-file /bats-libraries/bats-file

RUN curl -sSfLO --retry 8 --retry-all-errors --connect-timeout 10 --retry-delay 5 \
    https://get.helm.sh/helm-v3.18.6-linux-${BUILDARCH}.tar.gz && \
    tar -zxvf helm-v3*${BUILDARCH}.tar.gz && mv linux-${BUILDARCH}/helm /usr/bin

RUN curl -sSfLO --retry 8 --retry-all-errors --connect-timeout 10 --retry-delay 5 \
    https://dl.k8s.io/release/v1.34.0/bin/linux/${BUILDARCH}/kubectl && \
    chmod ugo+x kubectl && mv kubectl /usr/bin
