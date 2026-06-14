import axios from 'axios';
import type {
  ConfigList,
  ConfigEnvelope,
  ValidateResp,
  BinaryList,
  AvailableList,
  BinaryItem,
  AutoUpdateSettings,
  AutoUpdateView,
  TrafficSeries,
  LiveStatus,
  Projection,
  CFAccountList,
  CFAccountView,
  CFAccountResp,
  CFAccount,
  CFTunnelList,
  CFTunnel,
  CFConfigurationResult,
  CFTunnelConfig,
  CFConnectorList,
  CFZoneList,
  CFDNSList,
  CFDNSRecord,
  CFTokenInfo,
  CFBinding,
  CFPublicHostnameList,
  CFPublicHostnameWriteResp,
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

// ── 二进制自动更新 API ─────────────────────────────────────────────────────────
// 全部 snake_case。PUT 为部分更新（后端在当前设置上覆盖所发字段，DisallowUnknownFields
// 严格——只能发 AutoUpdateSettings 里的 key）。run 异步触发，202 立即返回，进度走
// binary.update 事件 + status 轮询。
export const autoUpdateApi = {
  get: () => client.get<AutoUpdateView>('/api/v1/binaries/auto-update'),
  update: (patch: Partial<AutoUpdateSettings>) =>
    client.put<AutoUpdateView>('/api/v1/binaries/auto-update', patch),
  run: (opts?: { version?: string; apply?: boolean; force?: boolean }) =>
    client.post('/api/v1/binaries/auto-update/run', opts || {}),
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

// ── Cloudflare 账号直连集成 API ───────────────────────────────────────────────
// 账号 secret 永不回传；隧道「配置」(ingress/originRequest) 走 camelCase 直传 CF。

export interface CFAccountInput {
  name: string;
  auth_type: 'token' | 'key';
  token?: string;
  email?: string;
  api_key?: string;
  account_id?: string;
}

export const cfApi = {
  // 账号
  listAccounts: () => client.get<CFAccountList>('/api/v1/cf/accounts'),
  getAccount: (aid: string) => client.get<CFAccountView>(`/api/v1/cf/accounts/${encodeURIComponent(aid)}`),
  createAccount: (payload: CFAccountInput) => client.post<CFAccountResp>('/api/v1/cf/accounts', payload),
  updateAccount: (aid: string, payload: Partial<CFAccountInput>) =>
    client.patch<CFAccountResp>(`/api/v1/cf/accounts/${encodeURIComponent(aid)}`, payload),
  deleteAccount: (aid: string) => client.delete(`/api/v1/cf/accounts/${encodeURIComponent(aid)}`),
  verifyAccount: (aid: string) => client.post<CFAccountResp>(`/api/v1/cf/accounts/${encodeURIComponent(aid)}/verify`),
  listCFAccounts: (aid: string) =>
    client.get<{ items: CFAccount[] }>(`/api/v1/cf/accounts/${encodeURIComponent(aid)}/cf-accounts`),

  // 远端隧道
  listTunnels: (aid: string) => client.get<CFTunnelList>(`/api/v1/cf/accounts/${encodeURIComponent(aid)}/tunnels`),
  createTunnel: (aid: string, name: string, configSrc = 'cloudflare') =>
    client.post<CFTunnel>(`/api/v1/cf/accounts/${encodeURIComponent(aid)}/tunnels`, { name, config_src: configSrc }),
  getTunnel: (aid: string, tid: string) =>
    client.get<CFTunnel>(`/api/v1/cf/accounts/${encodeURIComponent(aid)}/tunnels/${encodeURIComponent(tid)}`),
  renameTunnel: (aid: string, tid: string, name: string) =>
    client.patch<CFTunnel>(`/api/v1/cf/accounts/${encodeURIComponent(aid)}/tunnels/${encodeURIComponent(tid)}`, { name }),
  deleteTunnel: (aid: string, tid: string) =>
    client.delete(`/api/v1/cf/accounts/${encodeURIComponent(aid)}/tunnels/${encodeURIComponent(tid)}`),
  tunnelToken: (aid: string, tid: string) =>
    client.get<{ token: string }>(`/api/v1/cf/accounts/${encodeURIComponent(aid)}/tunnels/${encodeURIComponent(tid)}/token`),
  getTunnelConfig: (aid: string, tid: string) =>
    client.get<CFConfigurationResult>(
      `/api/v1/cf/accounts/${encodeURIComponent(aid)}/tunnels/${encodeURIComponent(tid)}/configurations`
    ),
  putTunnelConfig: (aid: string, tid: string, config: CFTunnelConfig) =>
    client.put<CFConfigurationResult>(
      `/api/v1/cf/accounts/${encodeURIComponent(aid)}/tunnels/${encodeURIComponent(tid)}/configurations`,
      { config }
    ),
  listConnections: (aid: string, tid: string) =>
    client.get<CFConnectorList>(
      `/api/v1/cf/accounts/${encodeURIComponent(aid)}/tunnels/${encodeURIComponent(tid)}/connections`
    ),
  cleanupConnections: (aid: string, tid: string, clientId?: string) =>
    client.delete(`/api/v1/cf/accounts/${encodeURIComponent(aid)}/tunnels/${encodeURIComponent(tid)}/connections`, {
      params: clientId ? { client_id: clientId } : undefined,
    }),

  // zones / DNS
  listZones: (aid: string, name?: string) =>
    client.get<CFZoneList>(`/api/v1/cf/accounts/${encodeURIComponent(aid)}/zones`, {
      params: name ? { name } : undefined,
    }),
  listDNS: (aid: string, zid: string, name?: string) =>
    client.get<CFDNSList>(`/api/v1/cf/accounts/${encodeURIComponent(aid)}/zones/${encodeURIComponent(zid)}/dns_records`, {
      params: name ? { name } : undefined,
    }),
  createDNS: (aid: string, zid: string, rec: CFDNSRecord) =>
    client.post<CFDNSRecord>(
      `/api/v1/cf/accounts/${encodeURIComponent(aid)}/zones/${encodeURIComponent(zid)}/dns_records`,
      rec
    ),
  updateDNS: (aid: string, zid: string, rid: string, rec: CFDNSRecord) =>
    client.put<CFDNSRecord>(
      `/api/v1/cf/accounts/${encodeURIComponent(aid)}/zones/${encodeURIComponent(zid)}/dns_records/${encodeURIComponent(rid)}`,
      rec
    ),
  deleteDNS: (aid: string, zid: string, rid: string) =>
    client.delete(
      `/api/v1/cf/accounts/${encodeURIComponent(aid)}/zones/${encodeURIComponent(zid)}/dns_records/${encodeURIComponent(rid)}`
    ),

  // 实例绑定 + 公共主机名
  tokenInfo: (id: string) => client.get<CFTokenInfo>(`/api/v1/configs/${encodeURIComponent(id)}/cf/token-info`),
  getBinding: (id: string) => client.get<CFBinding>(`/api/v1/configs/${encodeURIComponent(id)}/cf/binding`),
  setBinding: (id: string, payload: { account_id: string; tunnel_id?: string }) =>
    client.put<CFBinding>(`/api/v1/configs/${encodeURIComponent(id)}/cf/binding`, payload),
  deleteBinding: (id: string) => client.delete(`/api/v1/configs/${encodeURIComponent(id)}/cf/binding`),
  listPublicHostnames: (id: string) =>
    client.get<CFPublicHostnameList>(`/api/v1/configs/${encodeURIComponent(id)}/cf/public-hostnames`),
  addPublicHostname: (
    id: string,
    payload: { hostname: string; service: string; path?: string; origin_request?: Record<string, unknown>; manage_dns?: boolean }
  ) => client.post<CFPublicHostnameWriteResp>(`/api/v1/configs/${encodeURIComponent(id)}/cf/public-hostnames`, payload),
  updatePublicHostname: (
    id: string,
    index: number,
    payload: { hostname: string; service: string; path?: string; origin_request?: Record<string, unknown>; manage_dns?: boolean }
  ) => client.put<CFPublicHostnameWriteResp>(`/api/v1/configs/${encodeURIComponent(id)}/cf/public-hostnames/${index}`, payload),
  deletePublicHostname: (id: string, index: number, deleteDns = false) =>
    client.delete(`/api/v1/configs/${encodeURIComponent(id)}/cf/public-hostnames/${index}`, {
      params: deleteDns ? { delete_dns: 'true' } : undefined,
    }),
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
