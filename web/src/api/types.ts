// 手写的前后端契约类型（cloudflared-manager）。
//
// 权威来源：
//   - internal/api/configs.go         (configEnvelope / createReq)
//   - internal/manager/instance.go    (Snapshot, snake_case JSON)
//   - internal/manager/manager.go     (MgrMeta, camelCase JSON)
//   - pkg/cfdconfig/tunnel.go         (TunnelConfigV1, camelCase)
//   - internal/cfdbin/store.go        (InstalledVersion → BinaryItem)
//   - internal/cfdbin/download.go     (AvailableRelease)
//   - internal/metrics/store_alerts.go(AlertRule / AlertEvent)
//
// snake_case：Snapshot / BinaryItem / AvailableRelease / TrafficPoint / AlertRule / AlertEvent
// camelCase：MgrMeta / TunnelConfigV1 子树

// ── 实例快照（snake_case）────────────────────────────────────────────────────
export interface Snapshot {
  id: string;
  name: string;
  path: string;
  log_path: string;
  state: 'stopped' | 'starting' | 'started' | 'stopping';
  last_error?: string;
  started_at?: string;
  stopped_at?: string;
  binary_version?: string;
  pid?: number;
  metrics_port?: number;
}

// ── 管理器元数据（camelCase）─────────────────────────────────────────────────
export interface MgrMeta {
  name: string;
  manualStart: boolean;
}

// ── cloudflared 隧道配置（camelCase，对应 pkg/cfdconfig.TunnelConfigV1）─────────
// token 模式：仅建模 connector 进程消费的参数。ingress / public-hostname /
// origin 配置在 Cloudflare Zero Trust dashboard 管理，不在此处。
export interface TunnelEdgeConfig {
  protocol?: 'auto' | 'http2' | 'quic';
  edgeIpVersion?: 'auto' | '4' | '6';
  edgeBindAddress?: string;
  region?: '' | 'us';
  postQuantum?: boolean;
}
export interface TunnelReliabilityConfig {
  retries?: number;
  gracePeriod?: string;
}
export interface TunnelLoggingConfig {
  logLevel?: string;
  transportLogLevel?: string;
}
export interface TunnelIdentityConfig {
  label?: string;
  tags?: Record<string, string>;
}
export interface TunnelConfigV1 {
  // token 永不由 API 回传（后端脱敏）；提交时留空表示"保持现有"。
  token?: string;
  edge?: TunnelEdgeConfig;
  reliability?: TunnelReliabilityConfig;
  logging?: TunnelLoggingConfig;
  identity?: TunnelIdentityConfig;
  advancedEnvOverrides?: Record<string, string>;
  binaryVersion?: string;
  // 未知字段透传保留（前向兼容）。
  [key: string]: unknown;
}

// ── 配置信封（GET /configs/{id} 响应）────────────────────────────────────────
export interface ConfigEnvelope extends Snapshot {
  config: TunnelConfigV1;
  cfdmgr: MgrMeta;
  // 是否已存储 token（明文不回传，仅此标志 + GET /configs/{id}/token 掩码）。
  has_token?: boolean;
}

// ── 列表响应 ─────────────────────────────────────────────────────────────────
export interface ConfigList {
  items: Snapshot[];
}

// ── 校验响应 ─────────────────────────────────────────────────────────────────
export interface ValidateResp {
  valid: boolean;
  errors?: string[];
  warnings?: string[];
}

// ── 令牌掩码（GET /configs/{id}/token）───────────────────────────────────────
export interface TokenInfo {
  has_token: boolean;
  masked: string;
  length: number;
}

// ── 日志条目（snake_case，对应 logtail.Entry）─────────────────────────────────
// WS /configs/{id}/logs/stream 的帧元素。seq/time/level/message/raw/source 必有
// （后端无 omitempty）；event/conn_index/tunnel_id/fields 可选。
export interface LogEntry {
  seq: number;
  time: string;
  level: string;
  message: string;
  event?: number;
  conn_index?: number;
  tunnel_id?: string;
  raw: string;
  fields?: Record<string, unknown>;
  source: string;
  [key: string]: unknown;
}

// WS /logs/stream 帧：{ entries: LogEntry[] }
export interface LogStreamFrame {
  entries: LogEntry[];
}

// ── 实例实时状态（snake_case，对应 metrics.LiveStatus）──────────────────────────
// GET /configs/{id}/live：按需抓 cloudflared /metrics，不落库。未运行仅 running=false。
export interface EdgeConnection {
  conn_index: number;
  location?: string;
  rtt?: number; // smoothed RTT，cloudflared 原生单位（仅标 RTT，不臆断 ms）
  lost_packets?: number;
}
export interface LiveStatus {
  running: boolean;
  scraped_at?: number;
  ha_connections: number;
  requests_total: number;
  request_errors: number;
  response_5xx: number;
  goroutines: number;
  resident_memory_bytes: number;
  version?: string;
  protocol?: string;
  connections: EdgeConnection[] | null; // 后端无 omitempty，空时为 null
  error?: string;
}

// ── 运行参数投影（GET /configs/{id}/projection）───────────────────────────────
// cfdflags 投影出的真实 TUNNEL_* env/argv。env 内 TUNNEL_TOKEN 已脱敏。
export interface Projection {
  env: Record<string, string>;
  argv: string[];
  binary_version: string;
  binary_path: string;
}

// ── 二进制管理（snake_case，对应 cfdbin.InstalledVersion）─────────────────────
export interface BinaryItem {
  version: string;
  path: string;
  sha256?: string;
  source_url?: string;
  mirror?: string;
  downloaded_at?: string;
  size_bytes?: number;
  verified?: boolean;
  is_active: boolean;
}

export interface BinaryList {
  items: BinaryItem[];
}

// ── 可下载版本（snake_case，对应 cfdbin.AvailableRelease）──────────────────────
export interface AvailableRelease {
  tag_name: string;
  published_at: string;
  html_url: string;
  asset_url?: string;
  sha256?: string;
}

export interface AvailableList {
  items: AvailableRelease[];
}

// ── 二进制自动更新（snake_case，对应 cfdupdate.Settings / Status）──────────────
// 模式：full 全自动（下载+激活+滚动重启）｜download 仅下载｜notify 仅提示
export type AutoUpdateMode = 'full' | 'download' | 'notify';

export interface AutoUpdateSettings {
  enabled: boolean;
  mode: AutoUpdateMode;
  interval_hours: number;
  include_prerelease: boolean;
  auto_rollback: boolean;
  keep_versions: number;
  health_grace_seconds: number;
}

export interface AutoUpdateStatus {
  state: string; // idle|checking|downloading|applying|restarting|rolling_back
  last_result: string; // up_to_date|updated|downloaded|notified|failed|rolled_back|""
  last_error?: string;
  last_check_at?: string; // RFC3339
  active_version?: string;
  latest_known?: string;
  pending_version?: string;
  in_progress: boolean;
}

export interface AutoUpdateView {
  settings: AutoUpdateSettings;
  status: AutoUpdateStatus;
}

// ── 历史流量（snake_case）────────────────────────────────────────────────────
// 注意：cloudflared 无 per-tunnel 字节计数器；in = 请求数增量，out = 错误数增量，
// conns = HA 连接数（非字节）。
export interface TrafficPoint {
  ts: number;
  in: number;
  out: number;
  conns: number;
  inst_id?: string;
  scope?: string;
  key?: string;
}

export interface TrafficSeries {
  inst_id: string;
  scope: string;
  key: string;
  step: number;
  points: TrafficPoint[] | null;
}

// ── 告警（snake_case）────────────────────────────────────────────────────────
// conns = HA 连接数；requests_rate = 请求/秒；errors_rate = 错误/秒。
// （traffic_in_rate / traffic_out_rate 为旧别名，后端仍接受。）
export type AlertMetric =
  | 'conns'
  | 'requests_rate'
  | 'errors_rate'
  | 'traffic_in_rate'
  | 'traffic_out_rate';
export type AlertOp = '>' | '>=' | '<' | '<=';

export interface AlertRule {
  id: string;
  name: string;
  enabled: boolean;
  inst_id: string;
  metric: AlertMetric;
  op: AlertOp;
  threshold: number;
  for_seconds: number;
  target: string;
  webhook: string;
}

export interface AlertEvent {
  id: string;
  rule_id: string;
  inst_id: string;
  target: string;
  fired_at: number;
  resolved_at: number;
  value: number;
  state: 'firing' | 'resolved';
}

export interface AlertRuleList {
  items: AlertRule[];
}
export interface AlertEventList {
  items: AlertEvent[];
}

// ── Cloudflare 账号直连集成 ──────────────────────────────────────────────────
// 权威来源：
//   - internal/cfaccount/types.go (View, snake_case)
//   - internal/cfapi/types.go     (Account/Tunnel/Zone/DNSRecord… snake_case；
//                                   TunnelConfig 内 ingress/originRequest 为 camelCase)
//   - internal/api/cf_*.go        (响应信封)
//
// 注意大小写分界：账号视图 / 隧道 / zone / DNS 走 snake_case；隧道「配置」
// （ingress 规则 + originRequest 连接参数）走 cloudflared 原生 camelCase，原样直传 CF。

export type CFAuthType = 'token' | 'key';
export type CFAccountStatus = 'unverified' | 'active' | 'invalid';

// 脱敏后的账号视图（secret 永不回传，仅 has_token/has_key）。
export interface CFAccountView {
  id: string;
  name: string;
  auth_type: CFAuthType;
  account_id: string;
  account_name: string;
  email: string;
  has_token: boolean;
  has_key: boolean;
  status: CFAccountStatus;
  last_verified_at: string;
  created_at: string;
  updated_at: string;
}
// GET /accounts 返回的 Cloudflare 账号（用于多账号选择 account_id）。
export interface CFAccount {
  id: string;
  name: string;
  type?: string;
}
export interface CFAccountVerify {
  ok: boolean;
  error?: string;
  accounts?: CFAccount[];
}
export interface CFAccountResp {
  account: CFAccountView;
  verify: CFAccountVerify;
}
export interface CFAccountList {
  items: CFAccountView[];
}

// 远端隧道（cfd_tunnel，snake_case）。
export type CFTunnelStatus = 'inactive' | 'degraded' | 'healthy' | 'down';
export interface CFTunnel {
  id: string;
  account_tag?: string;
  name: string;
  created_at?: string;
  deleted_at?: string;
  conns_active_at?: string;
  conns_inactive_at?: string;
  status?: CFTunnelStatus;
  tun_type?: string;
  config_src?: 'local' | 'cloudflare';
  remote_config?: boolean;
  connections?: unknown;
}
export interface CFTunnelList {
  items: CFTunnel[];
}

// 隧道配置（ingress + originRequest，camelCase，原样直传 cloudflared/CF）。
export type CFOriginRequest = Record<string, unknown>;
export interface CFIngressRule {
  hostname?: string;
  service?: string;
  path?: string;
  originRequest?: CFOriginRequest;
}
export interface CFTunnelConfig {
  ingress?: CFIngressRule[];
  originRequest?: CFOriginRequest;
  'warp-routing'?: { enabled: boolean };
}
export interface CFConfigurationResult {
  account_id?: string;
  tunnel_id?: string;
  version?: number;
  config: CFTunnelConfig | null;
  source?: string;
  created_at?: string;
}

// 连接器（connections 端点，snake_case）。
export interface CFConnector {
  id: string;
  features?: string[];
  version?: string;
  arch?: string;
  run_at?: string;
  config_version?: number;
  conns?: unknown;
}
export interface CFConnectorList {
  items: CFConnector[];
}

// Zone / DNS 记录（snake_case）。
export interface CFZone {
  id: string;
  name: string;
  status?: string;
  paused?: boolean;
  account: { id: string; name: string };
}
export interface CFZoneList {
  items: CFZone[];
}
export interface CFDNSRecord {
  id?: string;
  type?: string;
  name?: string;
  content?: string;
  proxied?: boolean;
  ttl?: number;
  comment?: string;
  proxiable?: boolean;
  created_on?: string;
  modified_on?: string;
}
export interface CFDNSList {
  items: CFDNSRecord[];
}

// 实例 token 解码信息（无 secret）。
export interface CFTokenInfo {
  has_token: boolean;
  account_tag?: string;
  tunnel_id?: string;
  error?: string;
}

// 实例↔隧道绑定状态。
export interface CFBinding {
  bound: boolean;
  account_id?: string;
  account_name?: string;
  cf_account_id?: string;
  tunnel_id?: string;
  tunnel_name?: string;
  account_tag?: string;
  token_account_tag?: string;
  token_tunnel_id?: string;
  match: boolean;
  tunnel?: CFTunnel;
}

// 公共主机名聚合（ingress 规则 + DNS 记录状态）。
export interface CFDNSStatus {
  zone_id?: string;
  zone_name?: string;
  record_id?: string;
  content?: string;
  proxied: boolean;
  exists: boolean;
  in_sync: boolean;
}
export interface CFPublicHostname {
  index: number;
  hostname: string;
  service: string;
  path?: string;
  origin_request?: CFOriginRequest;
  dns?: CFDNSStatus;
}
export interface CFPublicHostnameList {
  items: CFPublicHostname[];
  tunnel_id?: string;
  dns_error?: string;
}
// 新增/编辑公共主机名（实例级聚合 API）的响应：除 hostname/service 外，带本次「代理 CNAME 同步」结果。
// dns_error 非空表示 DNS 同步失败（如 zone 不支持代理、token 无 DNS 权限等），需向用户提示。
export interface CFPublicHostnameWriteResp {
  hostname: string;
  service: string;
  index?: number;
  dns?: CFDNSStatus;
  dns_error?: string;
}
