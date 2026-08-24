# Build a static semidx binary, then run it on a minimal image that still has
# git + openssh-client (HTTPS/SSH tooling for server-side git-sync) and CA
# certificates (for cloud embedders).
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26.6 AS build
# Version metadata for `semidx version`. Without these the image reports
# dev/none/unknown, which makes a deployed container impossible to identify.
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Explicit package trees only (docker:S6470 — avoid recursive COPY . . which
# can pull secrets/docs into the build context even with .dockerignore).
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY pkg/ ./pkg/
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/semidx ./cmd/semidx

FROM alpine:3.20
# openssh-client provides the `ssh` binary for SSH clone/pull
# (GIT_SSH_COMMAND); git covers HTTPS. Vaulted per-project/host SSH creds
# use this once the job-runner resolution lands.
RUN apk add --no-cache ca-certificates git openssh-client && adduser -D -u 10001 semidx
COPY --from=build /out/semidx /usr/local/bin/semidx
COPY deploy/docker/semidx-entrypoint.sh /usr/local/bin/semidx-entrypoint.sh
COPY deploy/docker/semidx-migrate.sh /usr/local/bin/semidx-migrate.sh
RUN chmod 0555 /usr/local/bin/semidx-entrypoint.sh /usr/local/bin/semidx-migrate.sh \
    && mkdir -p /data /run/secrets \
    && chown -R semidx:semidx /data /run/secrets
USER semidx
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/semidx-entrypoint.sh"]
CMD ["serve"]
