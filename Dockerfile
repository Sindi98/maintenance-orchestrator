# Build the manager binary
FROM golang:1.22 AS builder
ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /workspace

# Cache dependencies first
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

# Copy sources
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/

# Build a static, stripped binary
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -a -trimpath -ldflags="-s -w" -o manager ./cmd/manager

# Minimal, non-root runtime image. The distroless nonroot user (uid 65532) and
# the world-readable/executable static binary make this run unchanged under the
# OpenShift "restricted-v2" SCC (arbitrary uid in group 0).
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager /manager
USER 65532:65532

ENTRYPOINT ["/manager"]
