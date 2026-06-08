# Release 代理 API 对接文档

> 面向第三方开发者的 GitHub Release 代理接口文档。
> 通过本服务可在**国内网络环境**下稳定地列出版本、查询版本信息、下载构建产物，无需直连 GitHub，也无需持有仓库的 GitHub Token。

- **文档版本**：v1.0
- **更新日期**：2026-06-06
- **协议**：HTTPS · UTF-8 · 仅 `GET`

---

## 1. 概览

本 API 在 Cloudflare Workers 边缘节点运行，作为 GitHub Release 的反向代理。每个对接方会被分配一个 **配置键（Key）**，对应后台预先配置好的某个 GitHub 仓库。你只需拿到 Key，即可调用以下三类接口：

| 能力 | 方法 | 路径 | 返回 |
|------|:----:|------|------|
| 列出所有版本 | `GET` | `/{key}` | JSON |
| 查询单个版本信息 | `GET` | `/{key}/{tag}` | JSON |
| 下载构建产物 | `GET` | `/{key}/{tag}/{filename}` | 二进制文件流 |

> 🔑 **配置键由服务方分配**。下文示例统一用 `myapp` 占位，对接时替换为你拿到的实际 Key。

---

## 2. 接入信息

### 2.1 基础域名（Base URL）

以下域名**全部指向同一服务、内容完全一致**，任选其一即可访问，互为备用。建议优先使用主域名，探测到不可用时自动切换到任意其他域名。

| Base URL | 用途 |
|----------|------|
| `https://gh-raw.966788.xyz` | 🟢 主域名 |
| `https://gh-raw.988669.xyz` | 🟢 主域名 |
| `https://gh-raw.s03.qzz.io` | 备用 |
| `https://gh-raw.s04.qzz.io` | 备用 |
| `https://gh-raw.s05.qzz.io` | 备用 |
| `https://gh-raw.s06.qzz.io` | 备用 |
| `https://gh-raw.s07.qzz.io` | 备用 |

> 共 7 个域名,两个 `.xyz` 为主域名,5 个 `.qzz.io` 为备用域名,均可正常访问。下文示例统一使用主域名 `gh-raw.966788.xyz`,替换为任意上述域名均等价。

完整请求地址 = `Base URL` + `接口路径`，例如：

```
https://gh-raw.966788.xyz/myapp/latest
```

### 2.2 鉴权（可选）

- **公开仓库 Key**：无需鉴权，直接调用。
- **受保护 Key**：服务方会在 Key 中内置一段访问令牌（AccessToken），此时所有路径需带上该令牌段：

  ```
  https://gh-raw.966788.xyz/myapp/{accessToken}/latest
  ```

  你**不需要**自己拼接令牌——列表/版本信息接口返回的 `download` 字段已包含完整、可直接使用的下载地址（含令牌）。

### 2.3 通用约定

| 项 | 说明 |
|----|------|
| **字符编码** | 全部 UTF-8 |
| **JSON 响应** | `Content-Type: application/json; charset=UTF-8` |
| **CORS** | 所有响应携带 `Access-Control-Allow-Origin: *`，支持浏览器跨域 `GET` |
| **缓存** | 见 [§6 缓存策略](#6-缓存策略)，响应通过标准 `Cache-Control` 头声明 |
| **时间格式** | ISO 8601（如 `2026-05-01T08:30:00Z`），透传自 GitHub |

---

## 3. 接口详情

### 3.1 列出所有版本

获取该仓库的全部 Release 列表（按发布时间倒序，与 GitHub 一致）。

```
GET /{key}
```

**查询参数**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|:----:|:----:|------|
| `per_page` | int | 否 | `30` | 返回的版本数量上限，最大 `100` |

**请求示例**

```bash
curl "https://gh-raw.966788.xyz/myapp?per_page=20"
```

**响应示例**（`200 OK`）

```json
{
  "repo": "owner/myapp",
  "count": 2,
  "releases": [
    {
      "tag": "v1.2.0",
      "name": "v1.2.0 稳定版",
      "prerelease": false,
      "draft": false,
      "published_at": "2026-05-01T08:30:00Z",
      "assets": [
        {
          "name": "myapp-linux-amd64.tar.gz",
          "size": 8123456,
          "download_count": 1287,
          "content_type": "application/gzip",
          "download": "https://gh-raw.966788.xyz/myapp/v1.2.0/myapp-linux-amd64.tar.gz"
        },
        {
          "name": "myapp-windows-amd64.zip",
          "size": 9210000,
          "download_count": 904,
          "content_type": "application/zip",
          "download": "https://gh-raw.966788.xyz/myapp/v1.2.0/myapp-windows-amd64.zip"
        }
      ]
    },
    {
      "tag": "v1.1.0",
      "name": "v1.1.0",
      "prerelease": false,
      "draft": false,
      "published_at": "2026-03-15T02:10:00Z",
      "assets": []
    }
  ]
}
```

---

### 3.2 查询单个版本信息

获取指定版本的详细信息，含发布说明（`body`）与 asset 清单。`tag` 可用特殊值 `latest` 自动解析为最新正式版本。

```
GET /{key}/{tag}
GET /{key}/latest
```

**路径参数**

| 参数 | 说明 |
|------|------|
| `tag` | 版本号（如 `v1.2.0`），或特殊值 `latest`（最新版本） |

**请求示例**

```bash
# 查询最新版本号 + asset 清单
curl https://gh-raw.966788.xyz/myapp/latest

# 查询指定版本
curl https://gh-raw.966788.xyz/myapp/v1.2.0
```

**响应示例**（`200 OK`）

```json
{
  "repo": "owner/myapp",
  "tag": "v1.2.0",
  "name": "v1.2.0 稳定版",
  "prerelease": false,
  "published_at": "2026-05-01T08:30:00Z",
  "body": "## 更新内容\n- 修复若干问题\n- 性能优化",
  "assets": [
    {
      "name": "myapp-linux-amd64.tar.gz",
      "size": 8123456,
      "download_count": 1287,
      "content_type": "application/gzip",
      "download": "https://gh-raw.966788.xyz/myapp/v1.2.0/myapp-linux-amd64.tar.gz"
    }
  ]
}
```

> 💡 即便请求的是 `latest`，返回的 `tag` 与每个 `download` 链接都会是**解析后的具体版本号**（如 `v1.2.0`），因此可放心缓存与分享。

---

### 3.3 下载构建产物

下载某个版本下的指定 asset 文件。响应为文件二进制流，浏览器中会触发下载。

```
GET /{key}/{tag}/{filename}
GET /{key}/latest/{filename}
```

**路径参数**

| 参数 | 说明 |
|------|------|
| `tag` | 版本号或 `latest` |
| `filename` | asset 的**完整文件名**，须与 `assets[].name` 完全一致 |

**请求示例**

```bash
# 下载指定版本的文件
curl -L https://gh-raw.966788.xyz/myapp/v1.2.0/myapp-linux-amd64.tar.gz -o app.tar.gz

# 下载最新版本的文件
curl -L https://gh-raw.966788.xyz/myapp/latest/myapp-linux-amd64.tar.gz -o app.tar.gz
```

> 推荐直接使用列表/版本信息接口返回的 `download` 字段，无需自己拼路径与文件名。

**响应**（`200 OK`，二进制流）

附带以下响应头：

| 响应头 | 说明 |
|--------|------|
| `Content-Type` | 文件 MIME 类型 |
| `Content-Length` | 文件字节数 |
| `Content-Disposition` | `attachment; filename="..."`，触发下载 |
| `Cache-Control` | 见 [§6](#6-缓存策略) |
| `X-Release-Tag` | 实际版本号 |
| `X-Release-Name` | 版本标题 |
| `X-Asset-Download-Count` | 该文件历史下载次数 |

---

## 4. 数据模型

### 4.1 Release 对象

| 字段 | 类型 | 说明 |
|------|------|------|
| `tag` | string | 版本号（GitHub `tag_name`） |
| `name` | string\|null | 版本标题 |
| `prerelease` | boolean | 是否为预发布版本 |
| `draft` | boolean | 是否为草稿（仅列表接口返回） |
| `published_at` | string | 发布时间（ISO 8601） |
| `body` | string | 发布说明 / Release Notes（仅单版本接口返回） |
| `assets` | Asset[] | 构建产物列表，可能为空数组 |

### 4.2 Asset 对象

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 文件名 |
| `size` | int | 文件字节数 |
| `download_count` | int | 历史下载次数 |
| `content_type` | string | 文件 MIME 类型 |
| `download` | string | **经本代理的完整下载地址**，可直接使用（含访问令牌，若有） |

---

## 5. 错误处理

> ⚠️ **注意**：错误响应为**纯文本**（`text/plain`，UTF-8），非 JSON。请依据 **HTTP 状态码** 进行编程判断，文本内容仅供人工排查。

| 状态码 | 含义 | 文本示例 |
|:------:|------|----------|
| `400` | 请求路径不合法 | `文件路径为空` |
| `401` | 访问令牌无效（受保护 Key） | `Access Token 无效` |
| `404` | Key 未配置 | `配置未找到` |
| `404` | 版本不存在 | `Release 未找到: v9.9.9` |
| `404` | 文件不存在（会附带可用文件列表，便于排查） | 见下方 |
| `5xx` | 上游 GitHub 异常 / 代理内部错误 | `获取 Release 信息失败: 503` / `Release 代理请求失败: ...` |

**文件不存在示例**（`404`）

```
Release "v1.2.0" 中未找到文件: app.zip

可用文件:
  - myapp-linux-amd64.tar.gz
  - myapp-windows-amd64.zip
```

**对接建议**

- `2xx` → 成功。
- `404` → 资源不存在，按业务提示用户（区分是 Key/版本/文件哪一级不存在可读文本）。
- `5xx` 或 `429`（见 §7）→ 视为临时故障，**切换备用域名**并按指数退避重试。

---

## 6. 缓存策略

服务通过标准 `Cache-Control` 声明缓存，并在 Cloudflare 边缘做缓存加速。对接方可据此设置本地/CDN 缓存。

| 资源 | Cache-Control | 说明 |
|------|---------------|------|
| 固定 `tag` 的 asset 下载 | `public, max-age=31536000, immutable` | 内容不可变，可长期缓存 |
| `latest` 的 asset 下载 | `public, max-age=300` | 最新版会变动，短缓存 |
| 列表 / 版本信息 JSON | `public, max-age=300`（固定 tag 信息 `max-age=86400`） | — |

> 因此，对固定版本号（非 `latest`）的下载链接，重复请求几乎都命中边缘缓存，速度快且不消耗上游配额。

---

## 7. 频率限制（Rate Limit）

- **公开仓库**：下载走 GitHub 公共下载通道，基本不受 API 配额限制；高频下载由边缘缓存承载。
- **私有仓库 / `latest` / 列表 / 版本信息**：依赖 GitHub API，受其配额约束。突发高频调用可能收到上游 `403/429`。

**建议**：

1. 列表 / 版本信息接口结果**自行缓存** ≥ 5 分钟，避免轮询。
2. 优先使用固定版本号的下载链接（可被边缘缓存）。
3. 遇到 `429/5xx` 时退避重试，并切换备用域名。

---

## 8. 接入示例

### 8.1 cURL

```bash
# 1) 取最新版本号
curl -s https://gh-raw.966788.xyz/myapp/latest | jq -r '.tag'

# 2) 取最新版第一个 asset 的下载地址
curl -s https://gh-raw.966788.xyz/myapp/latest | jq -r '.assets[0].download'

# 3) 下载
curl -L "$(curl -s https://gh-raw.966788.xyz/myapp/latest | jq -r '.assets[0].download')" -o app.bin
```

### 8.2 JavaScript（浏览器 / Node 18+）

```js
const BASE = 'https://gh-raw.966788.xyz';
const KEY = 'myapp';

// 获取最新版本及下载地址
async function getLatest() {
  const res = await fetch(`${BASE}/${KEY}/latest`);
  if (!res.ok) throw new Error(`查询失败: HTTP ${res.status}`);
  const data = await res.json();
  return {
    version: data.tag,
    notes: data.body,
    assets: data.assets.map(a => ({ name: a.name, url: a.download, size: a.size })),
  };
}

// 列出所有版本号
async function listVersions() {
  const res = await fetch(`${BASE}/${KEY}?per_page=50`);
  const data = await res.json();
  return data.releases.map(r => r.tag);
}
```

### 8.3 Python

```python
import requests

BASE = "https://gh-raw.966788.xyz"
KEY = "myapp"

def get_latest():
    r = requests.get(f"{BASE}/{KEY}/latest", timeout=10)
    r.raise_for_status()
    data = r.json()
    return data["tag"], data["assets"]

def download_latest_first_asset(dest="app.bin"):
    _, assets = get_latest()
    url = assets[0]["download"]
    with requests.get(url, stream=True, timeout=60) as resp:
        resp.raise_for_status()
        with open(dest, "wb") as f:
            for chunk in resp.iter_content(8192):
                f.write(chunk)
```

### 8.4 典型「检查更新」流程

```
1. GET /{key}/latest
2. 比较返回的 tag 与本地已安装版本
3. 若不同 → 取 assets 中匹配当前平台的项 → 用其 download 地址下载
4. 校验 size 后安装
```

---

## 9. 常见问题（FAQ）

**Q：`download` 字段和我自己拼的下载地址有什么区别？**
A：`download` 由服务端生成，已自动带上访问令牌（若该 Key 受保护）、并使用解析后的具体版本号。优先用它，避免拼错。

**Q：`latest` 包含预发布（prerelease）版本吗？**
A：不包含。`latest` 等价于 GitHub 的「Latest release」，只取最新正式版。需要预发布版请用具体 `tag`，可先用列表接口按 `prerelease` 字段筛选。

**Q：文件名带空格或特殊字符怎么办？**
A：直接使用 `download` 字段即可（已正确编码）。若手动拼接，请对文件名做 URL 编码。

**Q：错误响应为什么不是 JSON？**
A：当前错误体为纯文本，请以 **HTTP 状态码** 为准做逻辑判断，文本仅作人工排查参考。

**Q：这些域名有什么区别？**
A：全部完全等价、内容一致、互为备用。两个 `.xyz` 为主域名，5 个 `.qzz.io` 为备用域名，任选其一访问即可。建议主用 `gh-raw.966788.xyz`，故障时切换到任意其他域名。

---

## 10. 变更记录

| 版本 | 日期 | 说明 |
|------|------|------|
| v1.0 | 2026-06-06 | 首次发布：列出版本 / 查询版本信息 / 下载产物三类接口 |
