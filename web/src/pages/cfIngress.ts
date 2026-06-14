// CFConsole 公共主机名 / 隧道配置 共享逻辑。
//
// CFConsole 是按「账号 → 隧道」浏览的，未必有对应的本地实例，因此公共主机名
// 的增删改不能走实例级聚合 API（/configs/{id}/cf/public-hostnames），而是直接
// 操作远端隧道配置：getTunnelConfig → 改 ingress → putTunnelConfig；DNS 同步
// 在这一侧另用 listZones + createDNS 自行完成。
//
// 这里把这套「ingress 增删改 + DNS 同步」逻辑抽成纯函数复用，UI 只负责取值/渲染。

import { cfApi } from '../api/client';
import type { CFIngressRule, CFTunnelConfig, CFZone, CFOriginRequest, CFPublicHostname } from '../api/types';

// ── 服务类型 ─────────────────────────────────────────────────────────────────
// cloudflared service 字符串形如 http://localhost:8080 / tcp://localhost:22 /
// http_status:404 / unix:/path。这里把「协议」和「目标地址」拆成两段，再拼回。

export const SERVICE_TYPES = [
  { value: 'http', label: 'HTTP', scheme: 'http://' },
  { value: 'https', label: 'HTTPS', scheme: 'https://' },
  { value: 'tcp', label: 'TCP', scheme: 'tcp://' },
  { value: 'ssh', label: 'SSH', scheme: 'ssh://' },
  { value: 'rdp', label: 'RDP', scheme: 'rdp://' },
  { value: 'unix', label: 'UNIX Socket', scheme: 'unix:' },
  { value: 'smb', label: 'SMB', scheme: 'smb://' },
  { value: 'http_status', label: 'HTTP 状态码（兜底）', scheme: 'http_status:' },
] as const;

export type ServiceType = (typeof SERVICE_TYPES)[number]['value'];

// 把 service 字符串拆成 { type, target }，供回填编辑表单。
export function parseService(service: string): { type: ServiceType; target: string } {
  const s = (service || '').trim();
  if (s.startsWith('http_status:')) {
    return { type: 'http_status', target: s.slice('http_status:'.length) };
  }
  if (s.startsWith('unix:')) {
    return { type: 'unix', target: s.slice('unix:'.length) };
  }
  for (const t of SERVICE_TYPES) {
    if (t.scheme.endsWith('//') && s.startsWith(t.scheme)) {
      return { type: t.value, target: s.slice(t.scheme.length) };
    }
  }
  // 兜底：识别不了协议时按 http 处理，目标即原文。
  return { type: 'http', target: s };
}

// 把 { type, target } 拼成 service 字符串。
export function buildService(type: ServiceType, target: string): string {
  const def = SERVICE_TYPES.find((t) => t.value === type);
  const scheme = def?.scheme ?? 'http://';
  if (type === 'http_status') return `http_status:${(target || '404').trim()}`;
  if (type === 'unix') return `unix:${(target || '').trim()}`;
  return `${scheme}${(target || '').trim()}`;
}

// ── originRequest 表单 ────────────────────────────────────────────────────────
// 仅把用户实际填了的字段塞进 origin_request；空值（''/undefined/null）一律不带。
// access 子对象同理。布尔字段只在为 true 时带（cloudflared 默认 false）。

// 表单里 originRequest 区段的扁平字段集合（access 用 access_* 前缀打平）。
export interface OriginRequestFormValues {
  // 时长字段以「秒（数字）」表达：Cloudflare 隧道配置 API 的 originRequest 把这些
  // 时长按数字秒下发（默认 connectTimeout 30 / tlsTimeout 10 / tcpKeepAlive 30 /
  // keepAliveTimeout 90）。务必发 JSON 数字，发成字符串 "10" 会被 CF 以
  // strconv.ParseInt 解析失败（http 400 / 1056 Bad Configuration）。
  connectTimeout?: number;
  tlsTimeout?: number;
  tcpKeepAlive?: number;
  keepAliveTimeout?: number;
  keepAliveConnections?: number;
  noHappyEyeballs?: boolean;
  noTLSVerify?: boolean;
  disableChunkedEncoding?: boolean;
  http2Origin?: boolean;
  httpHostHeader?: string;
  originServerName?: string;
  caPool?: string;
  proxyType?: string;
  proxyAddress?: string;
  proxyPort?: number;
  access_required?: boolean;
  access_teamName?: string;
  access_audTag?: string[];
}

const STRING_KEYS: (keyof OriginRequestFormValues)[] = [
  'httpHostHeader',
  'originServerName',
  'caPool',
  'proxyType',
  'proxyAddress',
];
// 时长字段：CF API 要求「数字秒」。表单用 InputNumber（秒），发送时强制为整数数字。
const DURATION_KEYS: (keyof OriginRequestFormValues)[] = [
  'connectTimeout',
  'tlsTimeout',
  'tcpKeepAlive',
  'keepAliveTimeout',
];
const NUMBER_KEYS: (keyof OriginRequestFormValues)[] = ['keepAliveConnections', 'proxyPort'];
const BOOL_KEYS: (keyof OriginRequestFormValues)[] = [
  'noHappyEyeballs',
  'noTLSVerify',
  'disableChunkedEncoding',
  'http2Origin',
];

// 把时长值统一成「秒（数字）」。兼容三种来源：① 数字（已是秒）② 纯数字字符串
// "30" ③ 旧的 Go 时长字符串 "30s" / "1m30s" / "500ms"（历史保存的配置可能是这种）。
// 解析不出有效数字时返回 undefined（该字段不下发）。
function toSeconds(v: unknown): number | undefined {
  if (v == null || v === '') return undefined;
  if (typeof v === 'number') return Number.isFinite(v) ? v : undefined;
  const s = String(v).trim();
  if (s === '') return undefined;
  if (/^\d+(\.\d+)?$/.test(s)) return Number(s);
  const mult: Record<string, number> = { ns: 1e-9, us: 1e-6, 'µs': 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600 };
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  let total = 0;
  let matched = false;
  let m: RegExpExecArray | null;
  while ((m = re.exec(s)) !== null) {
    matched = true;
    total += Number(m[1]) * (mult[m[2]] ?? 1);
  }
  return matched ? total : undefined;
}

// 表单值 → originRequest（剔空）。无任何字段时返回 undefined。
export function buildOriginRequest(v: OriginRequestFormValues): CFOriginRequest | undefined {
  const out: Record<string, unknown> = {};
  for (const k of STRING_KEYS) {
    const val = v[k] as string | undefined;
    if (val != null && String(val).trim() !== '') out[k] = String(val).trim();
  }
  // 时长字段：转成整数秒的 JSON「数字」下发（CF 用 strconv.ParseInt 校验，发字符串会 400）。
  for (const k of DURATION_KEYS) {
    const n = toSeconds(v[k]);
    if (n != null) out[k] = Math.round(n);
  }
  for (const k of NUMBER_KEYS) {
    const val = v[k] as number | undefined;
    if (val != null && !Number.isNaN(val)) out[k] = val;
  }
  for (const k of BOOL_KEYS) {
    if (v[k]) out[k] = true;
  }
  // access 子对象
  const access: Record<string, unknown> = {};
  if (v.access_required) access.required = true;
  if (v.access_teamName && v.access_teamName.trim() !== '') access.teamName = v.access_teamName.trim();
  if (v.access_audTag && v.access_audTag.length > 0) access.audTag = v.access_audTag;
  if (Object.keys(access).length > 0) out.access = access;

  return Object.keys(out).length > 0 ? out : undefined;
}

// originRequest → 表单值（回填编辑）。
export function originRequestToForm(or?: CFOriginRequest): OriginRequestFormValues {
  const o = (or || {}) as Record<string, unknown>;
  const access = (o.access || {}) as Record<string, unknown>;
  const out: OriginRequestFormValues = {};
  for (const k of [...STRING_KEYS, ...NUMBER_KEYS, ...BOOL_KEYS]) {
    if (o[k] != null) (out as Record<string, unknown>)[k] = o[k];
  }
  // 时长字段回填成「秒（数字）」，兼容旧配置里存成 "30s" 字符串的情况。
  for (const k of DURATION_KEYS) {
    const n = toSeconds(o[k]);
    if (n != null) (out as Record<string, unknown>)[k] = n;
  }
  if (access.required != null) out.access_required = !!access.required;
  if (access.teamName != null) out.access_teamName = String(access.teamName);
  if (Array.isArray(access.audTag)) out.access_audTag = access.audTag.map((x) => String(x));
  return out;
}

// originRequest 概要标签（用于表格列展示）。
export function originRequestTags(or?: CFOriginRequest): string[] {
  const o = (or || {}) as Record<string, unknown>;
  const tags: string[] = [];
  if (o.noTLSVerify) tags.push('noTLSVerify');
  if (o.http2Origin) tags.push('http2Origin');
  if (o.noHappyEyeballs) tags.push('noHappyEyeballs');
  if (o.disableChunkedEncoding) tags.push('disableChunkedEncoding');
  if (o.httpHostHeader) tags.push(`Host: ${o.httpHostHeader}`);
  if (o.originServerName) tags.push(`SNI: ${o.originServerName}`);
  if (o.connectTimeout) tags.push(`connectTimeout=${o.connectTimeout}s`);
  const access = (o.access || {}) as Record<string, unknown>;
  if (access.required) tags.push('Access');
  return tags;
}

// ── ingress 操作 ──────────────────────────────────────────────────────────────
// 远端 ingress 数组最后一条通常是无 hostname 的兜底（如 http_status:404）。
// 「可编辑的公共主机名」= 带 hostname 的规则；兜底规则不展示为可编辑行。

// 是否兜底规则（无 hostname）。
export function isCatchAll(rule: CFIngressRule): boolean {
  return !rule.hostname || rule.hostname.trim() === '';
}

// 从配置取出可编辑的公共主机名规则（带其在原 ingress 中的下标）。
export function listHostnameRules(config: CFTunnelConfig | null): { rule: CFIngressRule; index: number }[] {
  const ingress = config?.ingress ?? [];
  const out: { rule: CFIngressRule; index: number }[] = [];
  ingress.forEach((rule, index) => {
    if (!isCatchAll(rule)) out.push({ rule, index });
  });
  return out;
}

// 确保 ingress 末尾有一条兜底规则；返回新数组（不改原数组）。
function ensureCatchAll(ingress: CFIngressRule[]): CFIngressRule[] {
  const rules = [...ingress];
  if (rules.length === 0 || !isCatchAll(rules[rules.length - 1])) {
    rules.push({ service: 'http_status:404' });
  }
  return rules;
}

// 在兜底规则之前插入一条新规则，返回新 config。
export function appendHostnameRule(config: CFTunnelConfig | null, rule: CFIngressRule): CFTunnelConfig {
  const base = config ?? {};
  const ingress = ensureCatchAll(base.ingress ?? []);
  // 找到第一条兜底规则的位置，插在它之前。
  const insertAt = ingress.findIndex((r) => isCatchAll(r));
  const at = insertAt === -1 ? ingress.length : insertAt;
  const next = [...ingress.slice(0, at), rule, ...ingress.slice(at)];
  return { ...base, ingress: next };
}

// 替换指定下标处的规则，返回新 config。
export function replaceRuleAt(config: CFTunnelConfig | null, index: number, rule: CFIngressRule): CFTunnelConfig {
  const base = config ?? {};
  const ingress = [...(base.ingress ?? [])];
  if (index < 0 || index >= ingress.length) return base;
  ingress[index] = rule;
  return { ...base, ingress: ensureCatchAll(ingress) };
}

// 按新的「公共主机名规则顺序」重排 ingress：把传入的有序 hostname 规则放前面，
// 兜底规则（http_status:404 等无 hostname 的）始终保留在末尾。cloudflared 按 ingress
// 顺序匹配，故顺序有意义；兜底必须最后。返回新 config（不改原数组）。
export function reorderHostnames(config: CFTunnelConfig | null, orderedRules: CFIngressRule[]): CFTunnelConfig {
  const base = config ?? {};
  const catchAll = (base.ingress ?? []).filter(isCatchAll);
  const tail = catchAll.length > 0 ? catchAll : [{ service: 'http_status:404' } as CFIngressRule];
  return { ...base, ingress: [...orderedRules, ...tail] };
}

// 删除指定下标处的规则，返回新 config。
export function removeRuleAt(config: CFTunnelConfig | null, index: number): CFTunnelConfig {
  const base = config ?? {};
  const ingress = [...(base.ingress ?? [])];
  if (index < 0 || index >= ingress.length) return base;
  ingress.splice(index, 1);
  return { ...base, ingress: ensureCatchAll(ingress) };
}

// ── DNS 同步 ──────────────────────────────────────────────────────────────────
// 为 hostname 创建/更新指向本隧道的代理 CNAME（{tid}.cfargotunnel.com）。
// zone 取「hostname 最长后缀匹配」的那个。失败抛错由调用方处理（DNS 同步非致命，
// 调用方一般 warning 而不阻断隧道配置保存）。

// 选 hostname 所属的 zone：name 是 hostname 的后缀，取最长匹配。
export function pickZone(hostname: string, zones: CFZone[]): CFZone | undefined {
  const h = (hostname || '').toLowerCase();
  let best: CFZone | undefined;
  for (const z of zones) {
    const zn = (z.name || '').toLowerCase();
    if (zn && (h === zn || h.endsWith('.' + zn))) {
      if (!best || zn.length > (best.name || '').length) best = z;
    }
  }
  return best;
}

// 同步代理 CNAME：在 hostname 对应 zone 建/改 {tid}.cfargotunnel.com 记录。
export async function syncProxyCNAME(aid: string, tid: string, hostname: string): Promise<void> {
  const content = `${tid}.cfargotunnel.com`;
  const zonesResp = await cfApi.listZones(aid);
  const zone = pickZone(hostname, zonesResp.data?.items ?? []);
  if (!zone) {
    throw new Error(`未找到 ${hostname} 对应的 zone，无法自动同步 DNS（请确认该域名已托管在此账号）`);
  }
  // 看是否已有同名记录。
  const existResp = await cfApi.listDNS(aid, zone.id, hostname);
  const existing = (existResp.data?.items ?? []).find((r) => (r.name || '').toLowerCase() === hostname.toLowerCase());
  const record = { type: 'CNAME', name: hostname, content, proxied: true, ttl: 1 };
  if (existing && existing.id) {
    await cfApi.updateDNS(aid, zone.id, existing.id, record);
  } else {
    await cfApi.createDNS(aid, zone.id, record);
  }
}

// 删除 hostname 的代理 CNAME（删除公共主机名时可选调用）。
export async function deleteProxyCNAME(aid: string, hostname: string): Promise<void> {
  const zonesResp = await cfApi.listZones(aid);
  const zone = pickZone(hostname, zonesResp.data?.items ?? []);
  if (!zone) return; // 找不到 zone 就当无需删除
  const existResp = await cfApi.listDNS(aid, zone.id, hostname);
  const existing = (existResp.data?.items ?? []).find((r) => (r.name || '').toLowerCase() === hostname.toLowerCase());
  if (existing && existing.id) {
    await cfApi.deleteDNS(aid, zone.id, existing.id);
  }
}

// ── 公共主机名表单值 ↔ API 形状 ───────────────────────────────────────────────
// PublicHostnameFormValues 是 PublicHostnameFormFields 组件用的扁平表单值：
// service 拆成 serviceType + serviceTarget，originRequest 各字段打平（access_* 前缀）。

export interface PublicHostnameFormValues extends OriginRequestFormValues {
  // hostname 现由「子域前缀 subdomain + 域名 zoneName」拼成（仿 Cloudflare 官方后台），
  // 提交时合成；回填时按账号 zone 列表拆分。hostname 保留为合成结果（可选）。
  hostname?: string;
  subdomain?: string;
  zoneName?: string;
  path?: string;
  serviceType: ServiceType;
  serviceTarget?: string;
  manage_dns?: boolean;
}

// 子域前缀 + 域名 → 完整主机名。subdomain 留空 = 直接用根域名（apex）。
export function buildHostname(subdomain?: string, zoneName?: string): string {
  const sub = (subdomain || '').trim().replace(/^\.+|\.+$/g, '');
  const zone = (zoneName || '').trim().replace(/^\.+|\.+$/g, '');
  if (!zone) return sub; // 未选域名时退回 sub（校验会拦住空 zone）
  return sub ? `${sub}.${zone}` : zone;
}

// 完整主机名 → { 子域前缀, 域名 }：按账号 zone 列表做最长后缀匹配。匹配不到时把
// 整个 hostname 当作 zoneName 兜底（保证编辑回填不丢值，且 Select 会注入该项）。
export function splitHostname(hostname: string, zones: CFZone[]): { subdomain: string; zoneName: string } {
  const raw = (hostname || '').trim();
  if (raw === '') return { subdomain: '', zoneName: '' };
  const h = raw.toLowerCase();
  const zone = pickZone(h, zones);
  if (!zone) return { subdomain: '', zoneName: raw };
  const zn = (zone.name || '').toLowerCase();
  if (h === zn) return { subdomain: '', zoneName: zone.name };
  if (h.endsWith('.' + zn)) return { subdomain: raw.slice(0, raw.length - zn.length - 1), zoneName: zone.name };
  return { subdomain: '', zoneName: zone.name };
}

// 实例级聚合 API（addPublicHostname/updatePublicHostname）的请求体。
export interface PublicHostnamePayload {
  hostname: string;
  service: string;
  path?: string;
  origin_request?: Record<string, unknown>;
  manage_dns?: boolean;
}

// 表单值 → 实例级聚合 API 请求体。
export function formToPayload(v: PublicHostnameFormValues): PublicHostnamePayload {
  const payload: PublicHostnamePayload = {
    hostname: buildHostname(v.subdomain, v.zoneName) || (v.hostname || '').trim(),
    service: buildService(v.serviceType, v.serviceTarget || ''),
    manage_dns: !!v.manage_dns,
  };
  if (v.path && v.path.trim() !== '') payload.path = v.path.trim();
  const or = buildOriginRequest(v);
  if (or) payload.origin_request = or as Record<string, unknown>;
  return payload;
}

// 表单值 → ingress 规则（CFConsole 直改远端隧道配置用）。
export function formToIngressRule(v: PublicHostnameFormValues): CFIngressRule {
  const rule: CFIngressRule = {
    hostname: buildHostname(v.subdomain, v.zoneName) || (v.hostname || '').trim(),
    service: buildService(v.serviceType, v.serviceTarget || ''),
  };
  if (v.path && v.path.trim() !== '') rule.path = v.path.trim();
  const or = buildOriginRequest(v);
  if (or) rule.originRequest = or;
  return rule;
}

// ingress 规则 → 表单值（CFConsole 编辑回填）。
export function ingressRuleToForm(rule: CFIngressRule): PublicHostnameFormValues {
  const { type, target } = parseService(rule.service || '');
  return {
    hostname: rule.hostname || '',
    path: rule.path,
    serviceType: type,
    serviceTarget: target,
    manage_dns: true,
    ...originRequestToForm(rule.originRequest),
  };
}

// 聚合公共主机名条目 → 表单值（实例级面板编辑回填）。
export function publicHostnameToForm(ph: CFPublicHostname): PublicHostnameFormValues {
  const { type, target } = parseService(ph.service || '');
  return {
    hostname: ph.hostname || '',
    path: ph.path,
    serviceType: type,
    serviceTarget: target,
    manage_dns: true,
    ...originRequestToForm(ph.origin_request),
  };
}
