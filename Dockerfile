# --------------------------------------
# STAGE 1: Build the manager (operator)
# --------------------------------------
    FROM golang:1.22 AS manager-builder
    ARG TARGETOS
    ARG TARGETARCH
    
    WORKDIR /workspace
    
    # Copy Go Modules manifests
    COPY go.mod go.mod
    COPY go.sum go.sum
    RUN go mod download
    
    # Copy your operator code (KubeBuilder standard layout)
    COPY cmd/ cmd/
    COPY api/ api/
    COPY internal/controller/ internal/controller/
    
    # Build the manager
    RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
        go build -a -o manager cmd/main.go
    
    # --------------------------------------
    # STAGE 2: Final minimal image
    # --------------------------------------
    FROM gcr.io/distroless/static:nonroot
    
    WORKDIR /
    
    # Copy the manager from stage 1
    COPY --from=manager-builder /workspace/manager /manager
    
    # Copy the precompiled 'sledge' binary from your local build
    # Assumes you have placed the binary at the root of the build context, e.g. 'sledge-operator/sledge'
    COPY sledge /usr/local/bin/sledge
    COPY uipath-amar-ed501cd5a389.json /etc/creds/uipath-amar-ed501cd5a389.json

    # Set environment variable so sledge (and the operator) uses these creds
    ENV GOOGLE_APPLICATION_CREDENTIALS="/etc/creds/uipath-amar-ed501cd5a389.json"
    
    USER 65532:65532
    ENTRYPOINT ["/manager"]