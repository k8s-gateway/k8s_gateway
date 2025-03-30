FROM --platform=${BUILDPLATFORM} docker.io/library/golang:1.24 AS builder

ARG LDFLAGS

WORKDIR /build

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

COPY cmd/ cmd/
COPY *.go ./

ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0
ENV GO111MODULE=on
ENV GOARCH=$TARGETARCH
ENV GOOS=$TARGETOS

RUN go build -ldflags "${LDFLAGS}" -o coredns cmd/coredns.go

# Update CA Certs
FROM docker.io/library/alpine:3.21@sha256:a8560b36e8b8210634f77d9f7f9efd7ffa463e380b75e2e74aff4511df3ef88c AS certs

RUN apk --update --no-cache add ca-certificates

# Final Build
FROM scratch

COPY --from=certs /etc/ssl/certs /etc/ssl/certs
COPY --from=builder /build/coredns .

EXPOSE 53 53/udp
ENTRYPOINT ["/coredns"]
