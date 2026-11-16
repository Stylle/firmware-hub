FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.11-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/firmware-hub . \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/data-init ./cmd/data-init \
 && go run ./cmd/notices /out/licenses

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata su-exec \
 && addgroup -g 10001 hub && adduser -D -H -u 10001 -G hub hub \
 && mkdir /data && chown hub:hub /data
COPY --from=build /out/firmware-hub /out/data-init /usr/local/bin/
COPY --chmod=755 docker-entrypoint.sh /usr/local/bin/
COPY --from=build /out/licenses /usr/share/licenses/firmware-hub/dependencies
COPY LICENSE /usr/share/licenses/firmware-hub/LICENSE
ENV DATA_DIR=/data LISTEN_ADDR=:8080 TZ=Asia/Shanghai
WORKDIR /app
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
