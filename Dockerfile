# Two-stage build: golang:1.21 compiles the binary CGO-off, distroless
# static:nonroot ships the runtime. Multi-arch via buildx: BUILDPLATFORM
# stays on the runner's native arch so go build does not run under QEMU
# while GOOS/GOARCH cross-compile for the requested TARGETPLATFORM.

FROM --platform=$BUILDPLATFORM golang:1.21 AS build
WORKDIR /src

ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/pricing-metrics-aggregator ./cmd/pricing-metrics-aggregator

FROM gcr.io/distroless/static-debian11:nonroot
COPY --from=build /out/pricing-metrics-aggregator /usr/local/bin/pricing-metrics-aggregator
USER 65532:65532
EXPOSE 8082
ENTRYPOINT ["/usr/local/bin/pricing-metrics-aggregator"]
