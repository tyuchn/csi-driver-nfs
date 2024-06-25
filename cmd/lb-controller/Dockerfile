FROM google-go.pkg.dev/golang:1.22.4@sha256:6eceb84bdfef1ee092d11df3af8bd1f59caaf853ba271f4408d76d7edab4c21b AS builder
ARG STAGINGVERSION
WORKDIR /lb-controller
ADD . .
RUN make lb-controller BINDIR=/bin
# Start from Kubernetes Debian base.
FROM gke.gcr.io/debian-base:bullseye-v1.4.3-gke.11@sha256:2161116f8b5b448c82644f970848c0e36c9849a1262ef477016d9d99fbbe0528 AS debian
# go/gke-releasing-policies#base-images
# We use `gcr.io/distroless/base` because it includes glibc.
FROM gcr.io/distroless/base-debian11 AS distroless-base
# The distroless amd64 image has a target triplet of x86_64
FROM distroless-base AS distroless-amd64
ENV LIB_DIR_PREFIX=x86_64
# The distroless arm64 image has a target triplet of aarch64
FROM distroless-base AS distroless-arm64
ENV LIB_DIR_PREFIX=aarch64
FROM distroless-$TARGETARCH AS output-image
# Final build stage, create the real Docker image with ENTRYPOINT
FROM output-image
COPY --from=builder /bin/lb-controller /lb-controller
ENTRYPOINT ["/lb-controller"]