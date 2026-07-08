# Build a static semidx binary, then run it on a minimal image that still has
# git (for server-side git-sync) and CA certificates (for cloud embedders).
FROM golang:1.25.12 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/semidx ./cmd/semidx

FROM alpine:3.20
RUN apk add --no-cache git ca-certificates && adduser -D -u 10001 semidx
COPY --from=build /out/semidx /usr/local/bin/semidx
USER semidx
EXPOSE 8080
ENTRYPOINT ["semidx"]
CMD ["serve"]
