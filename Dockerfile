# Build a static semidx binary, then run it on a minimal image that still has
# git + openssh-client (for server-side git-sync over HTTPS and SSH) and CA
# certificates (for cloud embedders).
FROM node:22-alpine AS web
WORKDIR /src
COPY web/package.json web/package-lock.json ./web/
RUN cd web && npm ci
COPY web/ ./web/
RUN cd web && npm run build

FROM golang:1.26.5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/semidx ./cmd/semidx

FROM alpine:3.20
# openssh-client provides the `ssh` binary gitsync needs for SSH clone/pull
# with per-project/host credentials (GIT_SSH_COMMAND); git covers HTTPS.
RUN apk add --no-cache git openssh-client ca-certificates && adduser -D -u 10001 semidx
COPY --from=build /out/semidx /usr/local/bin/semidx
USER semidx
EXPOSE 8080
ENTRYPOINT ["semidx"]
CMD ["serve"]
