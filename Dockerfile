FROM alpine:3.21 AS downloader
ARG HELM_VERSION=3.17.3
ARG OCM_VERSION=0.23.0
ARG CURL="curl --retry 5 --fail-early --retry-all-errors -fLZs"

RUN apk add --no-cache --no-progress ca-certificates curl git && mkdir -p /pkg/bin
RUN ${CURL} https://get.helm.sh/helm-v${HELM_VERSION}-linux-amd64.tar.gz | tar -xz \
    && mv linux-amd64/helm /pkg/bin/helm \
    && rm -rf linux-amd64
RUN ${CURL} https://github.com/open-component-model/ocm/releases/download/v${OCM_VERSION}/ocm-${OCM_VERSION}-linux-amd64.tar.gz | tar -xz \
    && mv ocm /pkg/bin/ocm

################################################################################

FROM golang:1.24.3-alpine3.21 AS builder

RUN apk add --no-cache --no-progress ca-certificates gcc git make musl-dev

COPY . /src
ARG BININFO_BUILD_DATE BININFO_COMMIT_HASH BININFO_VERSION # provided to 'make install'
RUN make -C /src install PREFIX=/pkg GOTOOLCHAIN=local GO_BUILDFLAGS='-mod vendor'

################################################################################

FROM alpine:3.21

# upgrade all installed packages to fix potential CVEs in advance
# also remove apk package manager to hopefully remove dependency on OpenSSL 🤞
RUN apk upgrade --no-cache --no-progress \
  && apk add --no-cache --no-progress git yq \
  && apk del --no-cache --no-progress apk-tools alpine-keys alpine-release libc-utils

COPY --from=builder /etc/ssl/certs/ /etc/ssl/certs/
COPY --from=builder /etc/ssl/cert.pem /etc/ssl/cert.pem
COPY --from=builder /pkg/ /usr/
# make sure all binaries can be executed
RUN ocm-helm-toolbox --version 2>/dev/null

ARG BININFO_BUILD_DATE BININFO_COMMIT_HASH BININFO_VERSION
LABEL source_repository="https://github.com/sapcc/ocm-helm-toolbox" \
  org.opencontainers.image.url="https://github.com/sapcc/ocm-helm-toolbox" \
  org.opencontainers.image.created=${BININFO_BUILD_DATE} \
  org.opencontainers.image.revision=${BININFO_COMMIT_HASH} \
  org.opencontainers.image.version=${BININFO_VERSION}

COPY --from=downloader /pkg/ /usr/
RUN ocm --version 2>/dev/null && helm version -c 2>/dev/null
WORKDIR /
ENTRYPOINT [ "/usr/bin/ocm-helm-toolbox" ]
