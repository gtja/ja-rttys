FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS ui
WORKDIR /rttys/ui
COPY ui/package*.json ./
RUN npm ci
COPY ui/ .
RUN npm run build
ARG CI_RUN_ID=local
RUN test -n "$CI_RUN_ID" && (npm audit --audit-level=low --json > /npm-audit.json || (cat /npm-audit.json; exit 1))

FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS source
WORKDIR /rttys
ARG GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
COPY --from=ui /rttys/internal/server/assets/dist internal/server/assets/dist

FROM source AS verify
RUN apt-get update && apt-get install -y --no-install-recommends python3 openssl \
    && rm -rf /var/lib/apt/lists/*
RUN go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
ARG CI_RUN_ID=local
RUN test -n "$CI_RUN_ID" && mkdir /ci \
    && (go test ./... -skip TestRttysStress -count=1 -timeout=180s -json > /ci/go-test.json || (cat /ci/go-test.json; exit 1))
RUN go vet ./... && (govulncheck ./cmd/rttys > /ci/govulncheck.txt || (cat /ci/govulncheck.txt; exit 1))
RUN go build -o /tmp/rttys-tenant ./cmd/rttys \
    && go build -o /tmp/rttys-socket-check ./cmd/tenant-e2e \
    && (RTTY_RUN_SUMMARY=/ci/e2e.json python3 scripts/run-local-e2e.py > /ci/e2e-run.log || (cat /ci/e2e-run.log; exit 1))
COPY --from=ui /npm-audit.json /ci/npm-audit.json

FROM source AS binary
ARG TARGETOS
ARG TARGETARCH
ARG SOURCE_COMMIT=unknown
ARG BUILD_TIME=unknown
RUN test -n "$TARGETOS" && test -n "$TARGETARCH" \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.GitCommit=$SOURCE_COMMIT -X main.BuildTime=$BUILD_TIME" -o /rttys-bin ./cmd/rttys

FROM alpine:3.22 AS runtime
ARG SOURCE_COMMIT=unknown
LABEL org.opencontainers.image.source="https://github.com/gtja/ja-rttys" \
      org.opencontainers.image.revision=$SOURCE_COMMIT
COPY --from=binary /rttys-bin /usr/bin/rttys
WORKDIR /data
VOLUME ["/data"]
ENTRYPOINT ["/usr/bin/rttys"]
CMD ["--tenant-db", "/data/identity.db", "--bootstrap-password-file", "/etc/rttys/bootstrap-password", "--sslcert", "/etc/rttys/cert.pem", "--sslkey", "/etc/rttys/key.pem"]
