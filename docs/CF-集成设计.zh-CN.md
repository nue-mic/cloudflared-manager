# Cloudflare 账号直连集成设计（CF Integration）

> 目标：在 cloudflared-manager 上叠加「Cloudflare 账号管理 + 直连官方 API 复刻隧道后台」能力。
> 用户配置一个或多个 CF 账号（API Token 或 邮箱+Global Key），自动解析 accountId；
> 给本地实例「关联」一个账号 + 远端隧道（带归属校验）；关联后解锁高级功能：
> 远端隧道增删改、公共主机名（ingress + DNS CNAME）、连接参数、连接器/连接查看、DNS 记录管理。
> 全部实时直连 `https://api.cloudflare.com/client/v4`，无需登录官方后台。

本文件是前后端对接的**权威字段规格**，命名规则严格遵守。

---

## 1. 后端分层

```
internal/cfapi/      纯 Cloudflare API HTTP 客户端（无存储、无业务）
  ├─ types.go        所有 CF 资源结构体（snake_case JSON，对齐官方）
  ├─ client.go       Client + 凭证 + envelope 解析 + 全部端点方法
  ├─ token.go        cloudflared connector token 本地解码（{a,t,s}）
  └─ errors.go       APIError（携带 CF errors[].code/message）
internal/cfaccount/  加密存储：CF 账号 + 实例绑定
  ├─ secret.go       AES-256-GCM + DEK 文件（secret.key）
  ├─ store.go        Store：账号/绑定 CRUD，原子落盘 cf-store.json
  └─ types.go        Account / Binding 持久化结构
internal/api/
  ├─ cf_accounts.go  账号 CRUD / 校验 / 列举 CF 账号
  ├─ cf_tunnels.go   远端隧道 CRUD / token / configurations / connections
  ├─ cf_dns.go       zones / dns_records CRUD
  └─ cf_link.go      实例↔账号绑定 / 归属校验 / 公共主机名聚合
```

## 2. 凭证与认证（CF 官方）

- **API Token**（推荐）：`Authorization: Bearer <token>`
- **Global API Key + Email**：`X-Auth-Email: <email>` + `X-Auth-Key: <key>`
- 校验 token：`GET /user/tokens/verify` → `result.status == "active"`
- 取当前用户（key 模式辅助）：`GET /user` → `result.email`
- 自动取 accountId：`GET /accounts` → `result[].{id,name,type}`（取第一个，或多账号时让用户选）

## 3. CF API 端点映射（client.go 方法）

基址 `https://api.cloudflare.com/client/v4`，统一 envelope：`{success,errors[],messages[],result,result_info}`。

| 方法 | HTTP | 路径 |
|---|---|---|
| VerifyToken | GET | `/user/tokens/verify` |
| GetUser | GET | `/user` |
| ListAccounts | GET | `/accounts` |
| ListTunnels | GET | `/accounts/{acc}/cfd_tunnel?is_deleted=false&per_page=100` |
| GetTunnel | GET | `/accounts/{acc}/cfd_tunnel/{tid}` |
| CreateTunnel | POST | `/accounts/{acc}/cfd_tunnel` `{name,config_src:"cloudflare"}` |
| UpdateTunnel | PATCH | `/accounts/{acc}/cfd_tunnel/{tid}` `{name?}` |
| DeleteTunnel | DELETE | `/accounts/{acc}/cfd_tunnel/{tid}` |
| GetTunnelToken | GET | `/accounts/{acc}/cfd_tunnel/{tid}/token` → result=string |
| GetConfiguration | GET | `/accounts/{acc}/cfd_tunnel/{tid}/configurations` |
| PutConfiguration | PUT | `/accounts/{acc}/cfd_tunnel/{tid}/configurations` `{config:{ingress,originRequest,warp-routing}}` |
| ListConnections | GET | `/accounts/{acc}/cfd_tunnel/{tid}/connections` |
| CleanupConnections | DELETE | `/accounts/{acc}/cfd_tunnel/{tid}/connections?client_id=` |
| ListZones | GET | `/zones?per_page=50&name=` |
| ListDNSRecords | GET | `/zones/{zid}/dns_records?per_page=100` |
| CreateDNSRecord | POST | `/zones/{zid}/dns_records` |
| UpdateDNSRecord | PUT | `/zones/{zid}/dns_records/{rid}` |
| DeleteDNSRecord | DELETE | `/zones/{zid}/dns_records/{rid}` |

### 3.1 Tunnel 资源（snake_case）
`id, account_tag, name, created_at, deleted_at, conns_active_at, conns_inactive_at,
status(inactive|degraded|healthy|down), tun_type, config_src(local|cloudflare),
remote_config(bool), connections[], metadata`

### 3.2 Configuration（ingress 配置）
响应 `{account_id, tunnel_id, version, config, source, created_at}`；`config` 为：
```jsonc
{
  "ingress": [
    { "hostname": "app.example.com", "service": "http://localhost:8080",
      "path": "/api/*", "originRequest": { ... } },
    { "service": "http_status:404" }            // 必须的兜底规则
  ],
  "originRequest": { ... },                       // 全局默认连接参数
  "warp-routing": { "enabled": false }
}
```
**originRequest 字段（camelCase，原样直传 CF）**：
`connectTimeout, tlsTimeout, tcpKeepAlive, noHappyEyeballs, keepAliveConnections,
keepAliveTimeout, httpHostHeader, originServerName, matchSNItoHost, caPool, noTLSVerify,
disableChunkedEncoding, http2Origin, proxyType, proxyAddress, proxyPort,
access{required, teamName, audTag[]}`

### 3.3 Connection / Client
`{id(connector/client id), features[], version, arch, conns[], run_at, config_version}`

### 3.4 Zone / DNS Record
Zone：`{id, name, status, account{id,name}, paused}`
DNS Record（CNAME 指向隧道）：
```jsonc
{ "type":"CNAME", "name":"app.example.com",
  "content":"<tunnel_id>.cfargotunnel.com", "proxied":true, "ttl":1, "comment":"..." }
```
响应额外：`id, proxiable, created_on, modified_on`

## 4. cloudflared token 解码（token.go）

connector token = base64(JSON)，JSON 形如 `{"a":"<accountTag>","t":"<tunnelID>","s":"<base64 secret>"}`。
`DecodeTunnelToken(tok) -> {AccountTag, TunnelID}`（兼容 std/url base64、有无 padding；忽略 secret）。
→ 关联校验核心：解码本地实例 token 得到 `accountTag/tunnelID`，与所选账号的 CF `accountId` 比对，
再 `GetTunnel(acc,tid)` 确认存在且未删除，即「这个隧道确属这个账号」。

## 5. 加密存储（cfaccount）

- DEK：`$DATA_DIR/secret.key`，32 字节随机，首次自动生成（0600）。
- 加密：AES-256-GCM，密文格式 `enc:v1:<base64(nonce|ciphertext)>`；非该前缀视为明文（向后兼容/导入）。
- 文件：`$DATA_DIR/cf-store.json`，原子写（tmp+rename），结构：
```jsonc
{ "version":1,
  "accounts":[ { "id","name","auth_type":"token|key","account_id","account_name",
                 "email","secret_token(enc)","secret_key(enc)",
                 "status":"unverified|active|invalid","last_verified_at","created_at","updated_at" } ],
  "bindings": { "<instanceID>": { "account_id":"<local acc id>","tunnel_id","tunnel_name","account_tag","linked_at" } } }
```
- 实例删除 → 通过订阅 eventbus `config.deleted` 清理对应 binding。

## 6. HTTP API（本项目，全部 `Bearer` 鉴权；secret 永不回传）

### 6.1 账号
| HTTP | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/cf/accounts` | 列出（脱敏：`has_token/has_key`，不含 secret） |
| POST | `/api/v1/cf/accounts` | 新建（自动校验+取 accountId）`{name,auth_type,token?,email?,api_key?,account_id?}` |
| GET | `/api/v1/cf/accounts/{aid}` | 详情（脱敏） |
| PATCH | `/api/v1/cf/accounts/{aid}` | 改名/换凭证（空 secret=保留） |
| DELETE | `/api/v1/cf/accounts/{aid}` | 删除 |
| POST | `/api/v1/cf/accounts/{aid}/verify` | 重新校验，刷新 status/account_name |
| GET | `/api/v1/cf/accounts/{aid}/cf-accounts` | 该凭证可见的 CF 账号列表（多账号选择） |

账号响应（脱敏）：
```jsonc
{ "id","name","auth_type","account_id","account_name","email",
  "has_token":bool,"has_key":bool,"status","last_verified_at","created_at","updated_at" }
```

### 6.2 远端隧道 / 配置 / 连接（经账号代理）
| HTTP | 路径 |
|---|---|
| GET/POST | `/api/v1/cf/accounts/{aid}/tunnels` |
| GET/PATCH/DELETE | `/api/v1/cf/accounts/{aid}/tunnels/{tid}` |
| GET | `/api/v1/cf/accounts/{aid}/tunnels/{tid}/token` |
| GET/PUT | `/api/v1/cf/accounts/{aid}/tunnels/{tid}/configurations` |
| GET | `/api/v1/cf/accounts/{aid}/tunnels/{tid}/connections` |
| DELETE | `/api/v1/cf/accounts/{aid}/tunnels/{tid}/connections?client_id=` |

### 6.3 zones / DNS
| HTTP | 路径 |
|---|---|
| GET | `/api/v1/cf/accounts/{aid}/zones?name=` |
| GET/POST | `/api/v1/cf/accounts/{aid}/zones/{zid}/dns_records` |
| PUT/DELETE | `/api/v1/cf/accounts/{aid}/zones/{zid}/dns_records/{rid}` |

### 6.4 实例绑定 + 公共主机名聚合（复刻后台核心）
| HTTP | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/configs/{id}/cf/token-info` | 解码本地 token → `{account_tag,tunnel_id}`（无 secret） |
| GET | `/api/v1/configs/{id}/cf/binding` | 当前绑定 + 远端隧道概要 |
| PUT | `/api/v1/configs/{id}/cf/binding` | 设绑定 `{account_id, tunnel_id?}`（缺省用 token 解出的 tid）；做归属校验 |
| DELETE | `/api/v1/configs/{id}/cf/binding` | 解绑 |
| GET | `/api/v1/configs/{id}/cf/public-hostnames` | 聚合：ingress 规则 + 对应 DNS 记录状态 |
| POST | `/api/v1/configs/{id}/cf/public-hostnames` | 增：插入 ingress 规则 + 建/改代理 CNAME |
| PUT | `/api/v1/configs/{id}/cf/public-hostnames/{index}` | 改第 index 条 ingress（+同步 DNS） |
| DELETE | `/api/v1/configs/{id}/cf/public-hostnames/{index}` | 删（+可选删 DNS） |

绑定响应：
```jsonc
{ "bound":bool, "account_id","account_name","tunnel_id","tunnel_name","account_tag",
  "token_account_tag","token_tunnel_id","match":bool, "tunnel":{...CF tunnel snapshot...} }
```

公共主机名条目：
```jsonc
{ "index":int,"hostname","service","path","origin_request":{...},
  "dns":{ "zone_id","zone_name","record_id","proxied","content","exists":bool,"in_sync":bool } }
```

## 7. 前端

- 新页面 `Cloudflare 账号`（`/cf/accounts`）：账号 CRUD + 校验状态 + 多账号选择。
- 新页面 `Cloudflare 后台`（`/cf/console`）：选账号 → 隧道列表 → 选隧道 → 三个 Tab：
  - **公共主机名**（表格 + 弹窗，含全部 originRequest 参数 + DNS 代理开关）
  - **隧道配置**（全局 originRequest + warp-routing）
  - **连接器/连接**（只读 + 清理）
  - DNS 记录浏览（按 zone）。
- 实例编辑页（Configs）新增「Cloudflare 关联」面板：自动解码 token → 匹配/选择账号 → 校验 → 绑定；
  已绑定则直达该实例的公共主机名管理。

## 8. 安全红线

- secret（token/api_key）：AES-GCM 落盘，**任何响应都不回传**；仅 `has_*` 与掩码预览。
- 所有新端点走既有 `middleware.Bearer`。
- CF API 调用超时 20s、`DisallowUnknownFields` 仅用于本项目入参，CF 响应用宽松解码。
