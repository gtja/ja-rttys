ARG NODE_BUILD_IMAGE=registry.jingan.com:32008/ja/node:22-bookworm-slim
ARG GO_BUILD_IMAGE=registry.jingan.com:32008/ja/ja-rttys-build:go1.27.3-vuln1.9.0-v1
ARG RUNTIME_IMAGE=registry.jingan.com:32008/ja/alpine:3.22
FROM --platform=$BUILDPLATFORM ${NODE_BUILD_IMAGE} AS ui
WORKDIR /rttys/ui
COPY ui/package*.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY ui/ .
RUN npm test
RUN npm run build
ARG CI_RUN_ID=local
RUN test -n "$CI_RUN_ID" && (npm audit --audit-level=low --json > /npm-audit.json || (cat /npm-audit.json; exit 1))

FROM --platform=$BUILDPLATFORM ${GO_BUILD_IMAGE} AS source
WORKDIR /rttys
ARG GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify
COPY . .
COPY --from=ui /rttys/internal/server/assets/dist internal/server/assets/dist

FROM source AS verify
ARG CI_RUN_ID=local
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    test -n "$CI_RUN_ID" && mkdir /ci \
    && (go test ./... -skip TestRttysStress -count=1 -timeout=180s -json > /ci/go-test.json || (cat /ci/go-test.json; exit 1))
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go vet ./... && (govulncheck ./cmd/rttys > /ci/govulncheck.txt || (cat /ci/govulncheck.txt; exit 1))
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -o /tmp/rttys-tenant ./cmd/rttys \
    && go build -o /tmp/rttys-socket-check ./cmd/tenant-e2e \
    && (RTTY_RUN_SUMMARY=/ci/e2e.json python3 scripts/run-local-e2e.py > /ci/e2e-run.log || (cat /ci/e2e-run.log; exit 1))
COPY --from=ui /npm-audit.json /ci/npm-audit.json

FROM source AS binary
ARG TARGETOS
ARG TARGETARCH
ARG SOURCE_COMMIT=unknown
ARG BUILD_TIME=unknown
ARG PACKAGE_VERSION=5.6.0
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    test -n "$TARGETOS" && test -n "$TARGETARCH" \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.GitCommit=$SOURCE_COMMIT -X main.BuildTime=$BUILD_TIME -X main.RttysVersion=$PACKAGE_VERSION" -o /rttys-bin ./cmd/rttys

FROM ${RUNTIME_IMAGE} AS runtime
ARG SOURCE_COMMIT=unknown
ARG PACKAGE_VERSION=5.6.0
LABEL org.opencontainers.image.source="https://github.com/gtja/ja-rttys" \
      org.opencontainers.image.revision=$SOURCE_COMMIT \
      org.opencontainers.image.version=$PACKAGE_VERSION
COPY --from=binary /rttys-bin /usr/bin/rttys
WORKDIR /data
VOLUME ["/data"]
ENTRYPOINT ["/usr/bin/rttys"]
CMD ["--tenant-db", "/data/identity.db", "--bootstrap-password-file", "/etc/rttys/bootstrap-password", "--sslcert", "/etc/rttys/cert.pem", "--sslkey", "/etc/rttys/key.pem"]
