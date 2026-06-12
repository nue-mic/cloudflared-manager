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
