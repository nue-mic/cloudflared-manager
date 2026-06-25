# deploy/ — Docker 部署资产

本目录用于在 Docker / docker compose 下跑 cloudflared-manager。文件清单：

| 文件 | 用途 |
|---|---|
| [`Dockerfile`](Dockerfile) | 三阶段构建（web build → go build → alpine runtime），产物是单二进制 `cfdmgrd` |
| [`docker-compose.yml`](docker-compose.yml) | 仓库本地构建 + 跑（开发/调试） |
| [`docker-compose.standalone.yml`](docker-compose.standalone.yml) | 直接拉 `ghcr.io/nue-mic/cloudflared-manager:<tag>` 镜像（生产推荐） |
| [`.env.example`](.env.example) | 环境变量模板（含 `CFDM_API_TOKEN` 等） |
| [`entrypoint.sh`](entrypoint.sh) | 容器入口脚本（设置数据目录权限 + 启动 cfdmgrd） |

> 配置环境变量请先 `cp .env.example .env` 并改 `CFDM_API_TOKEN`。完整环境变量见根 [README](../README.md#%EF%B8%8F-配置环境变量)。

---

## 常用命令

### 默认启动（本地构建）

```bash
docker-compose -p docker_cfdmgrd up --force-recreate --detach
```

### 指定启动文件

```bash
docker-compose -f ./docker-compose.yml -p docker_cfdmgrd up --force-recreate --detach
```

### 强制更新（每次都拉最新镜像）

```bash
docker-compose -f ./docker-compose.standalone.yml -p docker_cfdmgrd up --force-recreate --detach --pull always
```

### 生产推荐：用预构建镜像（免拉源码）

```bash
cp .env.example .env
# 编辑 .env: CFDM_API_TOKEN=$(openssl rand -hex 32)
docker compose -f docker-compose.standalone.yml up -d
docker compose -f docker-compose.standalone.yml logs -f
```

镜像位置：`ghcr.io/nue-mic/cloudflared-manager:<tag>`（amd64 + arm64 多架构），每个发布 tag 自动构建推送。

---

## 网络模式

**推荐 `network_mode: host`**。原因：管理器内每份 cloudflared 配置会 spawn 一个独立的 cloudflared worker 子进程，监听用户配置的端口。host 模式让这些端口直接对宿主机外可达，无需逐项 expose。

桥接模式也能跑，但每个实例的端口都得在 `ports:` 段显式声明，新建配置都要改 compose 文件并 recreate 容器，运维麻烦。

---

## 升级

```bash
docker compose -f docker-compose.standalone.yml pull   # 拉新镜像
docker compose -f docker-compose.standalone.yml up -d  # 重建容器（自动停旧起新）
```

数据目录 `/data`（含 `profiles/`、`logs/`、`metrics.db`、`meta.json`）由 volume 持久化，升级不丢配置和历史指标。

---

更多部署细节与故障排查见根 [README](../README.md)。
