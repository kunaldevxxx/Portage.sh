# ==============================================================================
# Portage Operator: Production Multi-Stage Distroless Container
# ==============================================================================

# Build Stage
FROM golang:1.22-alpine AS builder

WORKDIR /workspace

# Install ca-certificates and git if needed
RUN apk add --no-cache ca-certificates git

# Cache Go dependencies
COPY tools/portage/go.mod tools/portage/go.sum* ./
RUN go mod download

# Copy application source
COPY tools/portage/ ./

# Compile statically linked Linux binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -a -installsuffix cgo \
    -o portage-operator ./main.go

# Production Runtime Stage: Distroless Non-Root (FIPS & STIG Compliant)
FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /workspace/portage-operator /portage-operator

# Run as nonroot user (UID 65532)
USER 65532:65532

ENTRYPOINT ["/portage-operator"]
