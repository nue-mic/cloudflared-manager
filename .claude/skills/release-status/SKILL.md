---
name: release-status
description: Use this skill whenever the user asks about the project's release/CI/build/Docker status — "查看发布状态 / 发了吗 / CI 跑完没 / v1.2.x 出来了吗 / 等到发布完成 / release ready". Runs a self-contained shell script that snapshots all modules (CI runs / tags & assets / Docker image) or polls until a tag is fully released. Handles GitHub API rate limit (403), Windows Python GBK encoding, and git-bash path quirks automatically.
---

# release-status —— 全模块发布状态查询

> **触发即调用脚本，不要手搓 curl + jq**。脚本已封装本仓库踩过的所有坑（API 速率限、Windows 编码、临时路径、SSL 抖动），手搓只会重新踩一遍。

## 触发条件（命中任一即激活）

- 用户问发布/构建/CI/Docker 镜像状态：
  - "查看发布状态" / "状态怎么样了" / "发了吗"
  - "v1.2.x 出包了吗" / "release ready 了吗"
  - "CI 跑完没" / "actions 状态"
  - "等到发布完成告诉我" / "盯一下 release"
- 用户刚 `git push` 完，等 CI 出新版
- 用户问 Docker 镜像是否就绪

## 用法

### 快照模式（默认）

```bash
bash .claude/skills/release-status/check.sh
```

输出三表：
- **A — 最近 12 次 CI runs**：Lint / Tests / Release 的状态 + 结论 + commit
- **B — 最近 5 个 tag → release**：每个 tag 的资产数 + 发布状态（API 200 直读 / fallback HEAD 探测）；frps-manager 期望 14
- **C — Docker 镜像 (GHCR)**：镜像页可达性

### 跟踪模式（定时轮询）

```bash
bash .claude/skills/release-status/check.sh wait            # 默认跟最新 tag
bash .claude/skills/release-status/check.sh wait v1.2.X     # 指定 tag
```

每 30 秒查一次，最多 15 分钟，**release 完整发布（资产数 ≥ 19）后立即退出并打印下载页**。CI 仍在跑就显示 🔵；草稿状态显示 📝；超时则提示去 Actions 页手动查。

## 输出图例

| 标记 | 含义 |
|---|---|
| 🟢 | 完成 / 成功 |
| 🔵 | CI 进行中 |
| 🟡 | 部分完成（如 release 已建但资产不足） |
| 🔴 | 失败 / 资源缺失 |
| 📝 | 草稿 release |
| `~` | 资产数未知（API 速率限触发 HEAD fallback，已成功发布但不显示精确数） |

## 环境变量（可选）

| 变量 | 默认 | 用途 |
|---|---|---|
| `REPO` | `nue-mic/frps-manager` | 改成跨仓库（如查姊妹 `frpc-manager`） |
| `BIN_NAME` | `frpsmgrd` | 资产前缀；HEAD fallback 时用，跨仓库查时必须改 |
| `EXPECTED_ASSETS` | `14` | release 期望资产数（13 平台 tar.gz/zip + checksums.txt；frpc-manager 是 19） |

示例 — 反向查姊妹仓库 frpc-manager：
```bash
REPO=nue-mic/frpc-manager BIN_NAME=frpcmgrd bash .claude/skills/release-status/check.sh
```

## 设计要点（脚本内已实现，别绕过去）

1. **API 速率限 (403) 自动 fallback**：GitHub 匿名 API 上限 60 req/h，跑几次就被限。脚本检测到 403 自动改用 `HEAD` 探测 release page 与典型资产 URL：
   - release page 返回 200 → release 已发布
   - 资产 URL 返回 302 → 资产已上传
   - 这两条满足即可判定"完整发布"，资产数显示为 `~`
2. **Python JSON 必须 `encoding='utf-8'`**：Windows 默认 GBK，不指定会 `UnicodeDecodeError`。
3. **临时文件用 `mktemp` 而非 `/tmp` 硬编码**：git-bash 的 `/tmp` 是 `C:\Users\...\Temp`，Windows Python 不识别字面 `/tmp`。脚本用 `mktemp -d` + `cygpath -m` 转 Windows 风格再传给 python。
4. **curl 加 `--retry 2`**：国内访问 GitHub API 偶发 SSL peer failure，重试一两次几乎都能过。
5. **Tag 列表用 `git ls-remote`**：不占 API 配额，最稳。

## 何时**不**该用这个 skill

- 用户问"怎么发布"/"配置 CI" → 看 `.github/workflows/release.yml`，不查状态
- 用户已经在 GitHub Actions 网页看到了具体错误日志 → 直接看错误，不用这个工具

## 常见输出场景

**正常发布流（push 后 ~6 分钟）**：
```
A) Lint 🟢 success / Tests 🟢 success / Release 🔵 in_progress
B) vX.Y.Z-1 19 released  🟢   (上一版)
   vX.Y.Z    ~ no-assets 🟡   (release 已建, goreleaser 在编译)
```

**完整发布后**：
```
B) vX.Y.Z 19 released 🟢
```

**API 速率限触发 fallback**：
```
A) API 速率限 (403), 跳过 CI runs 表
B) vX.Y.Z ~ released 🟢 (HEAD 探测)
```
