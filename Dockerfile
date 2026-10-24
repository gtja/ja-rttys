FROM node:20 AS ui
WORKDIR /rttys/ui
COPY ui .
RUN npm ci && npm run build

FROM golang:1.27.3-bookworm AS rttys
WORKDIR /rttys
COPY . .
COPY --from=ui /rttys/internal/server/assets/dist internal/server/assets/dist
RUN CGO_ENABLED=0 \
    GitCommit=$(git log --pretty=format:"%h" -1) \
    BuildTime=$(date +%FT%T%z) \
    go build -ldflags="-s -w -X main.GitCommit=$GitCommit -X main.BuildTime=$BuildTime" -o rttys ./cmd/rttys

FROM alpine:latest
COPY --from=rttys /rttys/rttys /usr/bin/rttys
WORKDIR /data
VOLUME ["/data"]
ENTRYPOINT ["/usr/bin/rttys"]
CMD ["--tenant-db", "/data/identity.db", "--bootstrap-password-file", "/etc/rttys/bootstrap-password", "--sslcert", "/etc/rttys/cert.pem", "--sslkey", "/etc/rttys/key.pem"]
