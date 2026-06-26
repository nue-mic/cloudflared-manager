# syntax=docker/dockerfile:1.7
#
# 三阶段构建：
#   1. web-build  —— node:20-alpine 跑 vite build，产出 web/dist
#   2. build       —— golang:1.25-alpine 把 dist 接进来，go build 单二进制
#                     (前端通过 //go:embed dist 嵌入 cfdmgrd)
#   3. runtime     —— alpine:3.20 + su-exec，只装 cfdmgrd 这一个产物
#
# 设计目标：在任何环境（CI / 干净 git clone / 本地）都能直接
# `docker build -f deploy/Dockerfile .`，不需要任何前置 npm/make 步骤。
# .dockerignore 已排除 web/dist —— 永远从 web-build 阶段生成新鲜的产物。
#
# Multi-arch (amd64 + arm64) 关键优化：
#   web-build / build 阶段都用 --platform=$BUILDPLATFORM 锁在原生 CPU 上，
#   依赖 Go 自带的 GOOS/GOARCH 交叉编译产出目标架构二进制。否则 buildx
#   会用 QEMU 把 npm install 整套仿真出来，arm64 上慢 20-50 倍，
#   GitHub Actions 单次构建动辄 30 分钟仍未结束。

# ----- web build stage (永远跑在原生 CPU) -------------------------------
FROM --platform=$BUILDPLATFORM node:20-alpine AS web-build
WORKDIR /web

# 单独 COPY package*.json 让 npm ci 这一层尽量命中缓存。
# --legacy-peer-deps：openapi-typescript@7.x 声明 peer typescript@^5，
# 而本项目用 typescript@6.x —— 实际使用没冲突，但严格模式 npm ci 会拒绝。
# 与本地 package-lock.json 生成时使用的策略保持一致。
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund --legacy-peer-deps

# 拷剩余源码后 build
COPY web/ ./
RUN npm run build

# ----- go build stage (永远跑在原生 CPU，靠 Go 交叉编译产出目标架构) ----
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 用 web-build 阶段产物覆盖（dockerignore 已排除根上下文里的 web/dist），
# //go:embed dist 现在能拿到完整、最新的前端
COPY --from=web-build /web/dist ./web/dist

# buildx 多平台构建时会为每个 target 自动注入 TARGETOS / TARGETARCH；
# 单平台 docker build 默认 linux/amd64
ARG TARGETOS=linux
ARG TARGETARCH=amd64
# buildx 对 linux/arm/v7 会注入 TARGETVARIANT=v7；映射为 GOARM（v7→7, v6→6）。
# 非 arm 目标该值为空，go 自动忽略 GOARM。
ARG TARGETVARIANT=
ARG VERSION=dev
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} go build \
    -trimpath \
    -ldflags="-s -w \
      -X github.com/nue-mic/cloudflared-manager/pkg/version.Number=${VERSION} \
      -X github.com/nue-mic/cloudflared-manager/pkg/version.BuildDate=${BUILD_DATE}" \
    -o /out/cfdmgrd ./cmd/cfdmgrd

# ----- runtime stage ----------------------------------------------------
# alpine + su-exec, the same pattern the official postgres/redis images
# use: start as root, fix /data ownership, drop privileges, exec daemon.
# Trade ~5MB of base image size for a self-contained init flow (no
# sidecar, no host-side chown) that works for both named volumes and
# host bind mounts.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata su-exec \
    && addgroup -g 65532 -S nonroot \
    && adduser -u 65532 -G nonroot -S -D -H nonroot

COPY --from=build /out/cfdmgrd /usr/local/bin/cfdmgrd
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/cfdmgrd

ENV CFDM_DATA_DIR=/data \
    CFDM_HTTP_ADDR=:8080 \
    CFDM_LOG_LEVEL=info \
    CFDM_CORS_ORIGINS="*"

EXPOSE 8080
VOLUME ["/data"]

# Intentionally no USER directive — entrypoint.sh starts as root just
# long enough to chown /data, then re-execs as UID 65532 via su-exec.
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["serve"]
