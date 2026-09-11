# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /workspace

# Install build dependencies
RUN apk add --no-cache ca-certificates tzdata

# Copy go mod files
COPY go.mod go.mod
COPY go.sum go.sum

# Cache deps
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY api/ api/
COPY controllers/ controllers/
COPY conversion/ conversion/

# Build manager binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.version=v1beta1" \
    -a -installsuffix cgo \
    -o /output/manager ./cmd/main.go

# Final runtime stage
FROM gcr.io/distroless/static:nonroot

WORKDIR /

# Copy CA certificates from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy manager binary
COPY --from=builder /output/manager .

# Ports
EXPOSE 9443 8080 8081

# Security context
USER 65532:65532

ENTRYPOINT ["/manager"]
