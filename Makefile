SHELL := /bin/sh
VERSION ?= dev
BUILD_DATE := $(shell date -u +%Y-%m-%d)
LDFLAGS := -s -w \
    -X github.com/nue-mic/cloudflared-manager/pkg/version.Number=$(VERSION) \
    -X github.com/nue-mic/cloudflared-manager/pkg/version.BuildDate=$(BUILD_DATE)

.PHONY: build build-host web web-install test vet tidy clean docker run

# 前端依赖 — 仅在 node_modules 缺失时跑一次完整 install
web-install:
	test -d web/node_modules || (cd web && npm ci)

# 构建前端 dist —— 嵌入到 Go 二进制需要的产物
# 必须在 build / docker 之前执行；否则 //go:embed dist 会失败或得到空 FS
web: web-install
	cd web && npm run build

# Go 跨平台 (Linux/amd64) 构建 daemon —— 镜像里用这个
# 自动先 build web，确保 dist 是最新的
build: web
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "$(LDFLAGS)" -o bin/cfdmgrd ./cmd/cfdmgrd

# 本机平台构建（Windows/Mac/Linux 通用），用于本地开发跑 daemon
build-host: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/cfdmgrd ./cmd/cfdmgrd

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf bin/ web/dist/

# Docker 镜像构建：Dockerfile 自带 node:20 + golang:1.25 多阶段，
# 内部完成 npm build + go build。任何环境（本地 / CI / 干净 clone）
# 都可直接跑，无前置依赖。
docker:
	docker build -f deploy/Dockerfile -t cloudflared-manager:$(VERSION) \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg BUILD_DATE=$(BUILD_DATE) \
	  .

run: build-host
	CFDM_API_TOKEN=dev CFDM_DATA_DIR=./tmp/data ./bin/cfdmgrd serve
