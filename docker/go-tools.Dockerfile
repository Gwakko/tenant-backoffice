# syntax=docker/dockerfile:1
# Toolbox for tests, lint and codegen. Needs gcc for -race (present in the base image).
FROM golang:1.27.3-bookworm
ARG GOLANGCI_LINT_VERSION=v2.15.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOBIN=/usr/local/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}
RUN apt-get update && apt-get install -y --no-install-recommends make postgresql-client \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /workspace/backend
