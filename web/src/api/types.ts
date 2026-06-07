// 手写的前后端契约类型（cloudflared-manager）。
//
// 权威来源：
//   - internal/api/configs.go         (configEnvelope / createReq)
//   - internal/manager/instance.go    (Snapshot, snake_case JSON)
//   - internal/manager/manager.go     (MgrMeta, camelCase JSON)
//   - pkg/config/tunnel.go            (TunnelConfigV1, camelCase)
//   - internal/api/binaries.go        (BinaryItem / BinaryList)
//
// snake_case：Snapshot / LogEntry / TrafficPoint / AlertRule / AlertEvent
// camelCase：MgrMeta / TunnelConfigV1 / BinaryMeta

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
}

// ── 管理器元数据（camelCase）─────────────────────────────────────────────────
export interface MgrMeta {
  name: string;
  manualStart: boolean;
}

// ── cloudflared 隧道配置（camelCase，对应 TunnelConfigV1）──────────────────
// 极简子集；未知字段用索引签名保留（透传时不丢）。
export interface TunnelIngressRule {
  hostname?: string;
  path?: string;
  service: string;
  originRequest?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface TunnelConfigV1 {
  tunnel?: string;
  credentials_file?: string;
  token?: string;
  ingress?: TunnelIngressRule[];
  warp_routing?: { enabled?: boolean; [key: string]: unknown };
  [key: string]: unknown;
}

// ── 配置信封（GET /configs/{id} 响应）────────────────────────────────────────
export interface ConfigEnvelope extends Snapshot {
  config: TunnelConfigV1;
  cfdmgr: MgrMeta;
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

// ── 日志条目（snake_case）────────────────────────────────────────────────────
export interface LogEntry {
  time: string;
  level: string;
  message: string;
  source?: string;
  raw?: string;
  [key: string]: unknown;
}

// ── 二进制管理（snake_case）──────────────────────────────────────────────────
export interface BinaryItem {
  version: string;
  path: string;
  size: number;
  downloaded_at: string;
  is_active: boolean;
  verified?: boolean;
}

export interface BinaryList {
  items: BinaryItem[];
  active_version?: string;
}

export interface AvailableRelease {
  version: string;
  tag_name: string;
  published_at: string;
  download_url?: string;
  size?: number;
  pre_release?: boolean;
}

export interface BinaryMeta {
  available: AvailableRelease[];
  current_version?: string;
}

// ── 历史流量（snake_case）────────────────────────────────────────────────────
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
export type AlertMetric = 'conns' | 'traffic_in_rate' | 'traffic_out_rate';
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
