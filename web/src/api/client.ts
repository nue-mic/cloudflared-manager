import axios from 'axios';
import type {
  ConfigList,
  ConfigEnvelope,
  ValidateResp,
  BinaryList,
  AvailableList,
  BinaryItem,
  TrafficSeries,
  LiveStatus,
  Projection,
} from './types';

// localStorage key
const TOKEN_KEY = 'cfdmgr_api_token';

export const getAPIToken = (): string => {
  return localStorage.getItem(TOKEN_KEY) || '';
};

export const setAPIToken = (token: string) => {
  localStorage.setItem(TOKEN_KEY, token);
};

export const clearAPIToken = () => {
  localStorage.removeItem(TOKEN_KEY);
};

const client = axios.create({
  baseURL: '',
  timeout: 30000,
});

// 请求拦截器：自动注入 Bearer Token
client.interceptors.request.use(
  (config) => {
    const token = getAPIToken();
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
  },
  (error) => Promise.reject(error)
);

// 响应拦截器：统一处理 401
client.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response && error.response.status === 401) {
      if (
        !error.config.url?.includes('/api/v1/version') &&
        !error.config.url?.includes('/api/v1/health')
      ) {
        clearAPIToken();
        window.location.href = '/login';
      }
    }
    return Promise.reject(error);
  }
);

export default client;

// ── Configs API ──────────────────────────────────────────────────────────────

export const configsApi = {
  list: () => client.get<ConfigList>('/api/v1/configs'),
  get: (id: string) => client.get<ConfigEnvelope>(`/api/v1/configs/${id}`),
  create: (payload: object) => client.post('/api/v1/configs', payload),
  update: (id: string, payload: object) => client.put(`/api/v1/configs/${id}`, payload),
  delete: (id: string) => client.delete(`/api/v1/configs/${id}`),
  start: (id: string) => client.post(`/api/v1/configs/${id}/start`),
  stop: (id: string) => client.post(`/api/v1/configs/${id}/stop`),
  reload: (id: string) => client.post(`/api/v1/configs/${id}/reload`),
  duplicate: (id: string, newId: string) =>
    client.post(`/api/v1/configs/${id}/duplicate`, { new_id: newId }),
  status: (id: string) => client.get(`/api/v1/configs/${id}/status`),
};

// ── Binaries API ─────────────────────────────────────────────────────────────

export const binariesApi = {
  list: () => client.get<BinaryList>('/api/v1/binaries'),
  available: () => client.get<AvailableList>('/api/v1/binaries/available'),
  install: (version: string) =>
    client.post<BinaryItem>('/api/v1/binaries/install', { version }),
  // 后端路由是 POST /binaries/{version}/activate —— version 在路径里，不在 body。
  activate: (version: string) =>
    client.post(`/api/v1/binaries/${encodeURIComponent(version)}/activate`),
  delete: (version: string) => client.delete(`/api/v1/binaries/${encodeURIComponent(version)}`),
};

// ── Validate API ─────────────────────────────────────────────────────────────
export const validateApi = {
  validate: (content: string) =>
    client.post<ValidateResp>('/api/v1/validate', content, {
      headers: { 'Content-Type': 'text/plain' },
    }),
};

// ── Metrics / Traffic API ─────────────────────────────────────────────────────
// 历史流量曲线。注意 to 必填（unix 秒），缺省后端 400。字段语义（非字节）：
//   server scope     → in=请求数增量, out=错误数增量, conns=HA 连接数
//   edge_conn scope  → in=smoothed_rtt, out=lost_packets（key=conn_index 0..3）
export const metricsApi = {
  traffic: (
    id: string,
    params: { scope?: string; key?: string; from?: number; to: number; step?: number }
  ) => client.get<TrafficSeries>(`/api/v1/metrics/${encodeURIComponent(id)}/traffic`, { params }),
};

// ── 单实例实时状态 / 运行参数投影 ──────────────────────────────────────────────
export const instanceApi = {
  // 按需抓 cloudflared /metrics；未运行返回 {running:false}（HTTP 仍 200）。
  live: (id: string) =>
    client.get<LiveStatus>(`/api/v1/configs/${encodeURIComponent(id)}/live`),
  // cfdflags 投影出的真实 TUNNEL_* env/argv（token 已脱敏）。
  projection: (id: string) =>
    client.get<Projection>(`/api/v1/configs/${encodeURIComponent(id)}/projection`),
};

// 结构化实时日志 WS 地址。浏览器 WS 无法自定义 Header，故 token / 过滤走 query。
export function logStreamUrl(
  id: string,
  filter?: { level?: string; keyword?: string; connIndex?: number; backlog?: number }
): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const params = new URLSearchParams();
  const token = getAPIToken();
  if (token) params.set('token', token);
  if (filter?.level) params.set('level', filter.level);
  if (filter?.keyword) params.set('keyword', filter.keyword);
  if (filter?.connIndex != null) params.set('conn_index', String(filter.connIndex));
  if (filter?.backlog != null) params.set('backlog', String(filter.backlog));
  return `${proto}//${window.location.host}/api/v1/configs/${encodeURIComponent(
    id
  )}/logs/stream?${params.toString()}`;
}
