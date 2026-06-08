// 配置参考数据 —— cloudflared 隧道 (TunnelConfigV1) 全部 YAML 参数目录。
//
// 权威来源（逐字段对齐 Go 源，改这里前请同步）：
//   - pkg/cfdflags/flags.go        registry（YAMLPath/EnvName/Enum/Default/HelpText/Advanced）
//   - pkg/cfdflags/mapping.go      字段 → TUNNEL_* 投影
//   - pkg/cfdflags/whitelist.go    advancedEnvOverrides 白名单 + 保留键
//   - pkg/cfdconfig/tunnel.go      字段与文档
//   - pkg/cfdconfig/validate.go    精确约束/取值/默认
//
// 本项目是 token 模式：ingress / public-hostname / origin 配置在 Cloudflare
// Zero Trust dashboard 管理，这里只建模 connector 进程消费的参数 + token。

import { stringify as stringifyYaml } from 'yaml';

export interface FieldDef {
  /** 完整 YAML 路径，如 "edge.protocol"；顶层字段就是裸键如 "token" */
  path: string;
  type: string;
  /** 允许值 / 枚举；留空表示自由格式 */
  allowed?: string;
  /** 默认值（含 cloudflared 上游默认）；'—' 表示无 */
  default?: string;
  /** 校验约束（来自 validate.go）；留空表示无 */
  constraint?: string;
  /** 投影到的 TUNNEL_* 环境变量；'—' 表示不投影（如 label 走 --label argv） */
  env?: string;
  desc: string;
  /** 生成 YAML 片段用的示例值 */
  example: unknown;
  advanced?: boolean;
}

export interface CatalogGroup {
  key: string;
  /** 顶层 YAML 键（'' 表示字段直接挂在根） */
  yamlKey: string;
  title: string;
  desc: string;
  fields: FieldDef[];
}

export const CATALOG: CatalogGroup[] = [
  {
    key: 'top',
    yamlKey: '',
    title: '顶层 · 必填与全局',
    desc: 'token 是唯一必填项；binaryVersion 钉选该实例使用的 cloudflared 二进制版本。',
    fields: [
      {
        path: 'token',
        type: 'string',
        allowed: 'Cloudflare 隧道 token（base64）',
        default: '—（必填）',
        constraint: '启动前校验：长度 100–1500，仅 base64 字符集',
        env: 'TUNNEL_TOKEN（由 cfdmgrd 注入）',
        desc: 'cloudflared connector 连接令牌。响应中永不回传明文；编辑时留空表示保持现有 token。',
        example: '<粘贴你的 Cloudflare 隧道 token>',
      },
      {
        path: 'binaryVersion',
        type: 'string',
        allowed: 'current | 具体 tag（如 2026.5.2）',
        default: 'current',
        env: '—（用于解析二进制路径）',
        desc: 'current（或留空）跟随全局激活版本；填具体 tag 可为该实例独立钉版本（灰度/回滚）。',
        example: 'current',
        advanced: true,
      },
    ],
  },
  {
    key: 'edge',
    yamlKey: 'edge',
    title: 'edge · 边缘连接',
    desc: 'connector 如何连到 Cloudflare 边缘网络。',
    fields: [
      {
        path: 'edge.protocol',
        type: 'string',
        allowed: 'auto | http2 | quic',
        default: 'auto',
        env: 'TUNNEL_TRANSPORT_PROTOCOL',
        desc: 'connector 与边缘之间的传输协议。auto 优先 QUIC 并回退 HTTP/2。',
        example: 'auto',
      },
      {
        path: 'edge.edgeIpVersion',
        type: 'string',
        allowed: 'auto | 4 | 6',
        default: '4',
        env: 'TUNNEL_EDGE_IP_VERSION',
        desc: '拨号边缘使用的 IP 协议族。注意键名是 edgeIpVersion（不是 edgeIPVersion）。',
        example: '4',
      },
      {
        path: 'edge.region',
        type: 'string',
        allowed: '"" | us',
        default: '""（全球）',
        env: 'TUNNEL_REGION',
        desc: '限制边缘路由区域。空字符串=全球；目前 cloudflared 仅接受 us。',
        example: '',
      },
      {
        path: 'edge.edgeBindAddress',
        type: 'string',
        allowed: '本地 IP 地址',
        default: '—',
        constraint: '不得含空白字符',
        env: 'TUNNEL_EDGE_BIND_ADDRESS',
        desc: '固定出站到边缘的本地源 IP；其 IP 族会覆盖 edgeIpVersion。留空走 OS 默认。',
        example: '',
        advanced: true,
      },
      {
        path: 'edge.postQuantum',
        type: 'bool',
        allowed: 'true | false',
        default: 'false',
        constraint: '仅当 protocol=quic 时允许（否则校验 400）',
        env: 'TUNNEL_POST_QUANTUM',
        desc: '强制与边缘做抗量子密钥交换。',
        example: false,
        advanced: true,
      },
    ],
  },
  {
    key: 'reliability',
    yamlKey: 'reliability',
    title: 'reliability · 可靠性',
    desc: '重试与优雅停机行为。',
    fields: [
      {
        path: 'reliability.retries',
        type: 'int',
        allowed: '1–20',
        default: '5',
        constraint: '0–20（0=用 cloudflared 默认 5）',
        env: 'TUNNEL_RETRIES',
        desc: '放弃前的连接/协议重试次数（指数退避 1s,2s,4s…）。',
        example: 5,
      },
      {
        path: 'reliability.gracePeriod',
        type: 'duration',
        allowed: 'Go duration，如 30s / 2m',
        default: '30s',
        constraint: '1s – 5m',
        env: 'TUNNEL_GRACE_PERIOD',
        desc: '收到 SIGTERM/SIGINT 后，等待在途请求完成再退出的时长。',
        example: '30s',
      },
    ],
  },
  {
    key: 'logging',
    yamlKey: 'logging',
    title: 'logging · 日志',
    desc: '两个日志级别；输出流/格式（stderr + JSON）由 cfdmgrd 接管，不在此建模。',
    fields: [
      {
        path: 'logging.logLevel',
        type: 'string',
        allowed: 'debug | info | warn | error | fatal',
        default: 'info',
        env: 'TUNNEL_LOGLEVEL',
        desc: '应用级日志详尽度。debug 会记录请求 URL 与 header（敏感，谨慎）。',
        example: 'info',
      },
      {
        path: 'logging.transportLogLevel',
        type: 'string',
        allowed: 'debug | info | warn | error | fatal',
        default: 'info',
        env: 'TUNNEL_TRANSPORT_LOGLEVEL',
        desc: '传输层（QUIC/HTTP2）日志级别，与应用级独立。',
        example: 'info',
        advanced: true,
      },
    ],
  },
  {
    key: 'identity',
    yamlKey: 'identity',
    title: 'identity · 身份',
    desc: '上报到 Zero Trust dashboard 的 connector 身份信息。',
    fields: [
      {
        path: 'identity.label',
        type: 'string',
        allowed: '字符集 [A-Za-z0-9_-. 空格]',
        default: '—',
        constraint: '长度 ≤ 64',
        env: '—（无 TUNNEL_LABEL，走 --label argv）',
        desc: 'connector 显示名，dashboard 可见。这是唯一经 argv 传递的字段。',
        example: 'my-connector',
      },
      {
        path: 'identity.tags',
        type: 'map[string]string',
        allowed: 'key: [A-Za-z_][A-Za-z0-9_]* ；value 任意',
        default: '—',
        constraint: 'key ≤32，value ≤128',
        env: 'TUNNEL_TAG（拼成 k1=v1,k2=v2）',
        desc: '转发到 dashboard 的键值标注。',
        example: { env: 'prod', team: 'infra' },
        advanced: true,
      },
    ],
  },
  {
    key: 'advanced',
    yamlKey: 'advancedEnvOverrides',
    title: 'advancedEnvOverrides · 高级 env 逃生舱',
    desc: '直接注入白名单内的 cloudflared 环境变量；非白名单键在启动时被丢弃（并在「配置校验」给出警告）。',
    fields: [
      {
        path: 'advancedEnvOverrides',
        type: 'map[string]string',
        allowed: '仅白名单 TUNNEL_* 键（见下方白名单卡片）',
        default: '—',
        constraint: 'key 必须匹配 ^[A-Z][A-Z0-9_]*$ 且不在保留集',
        env: '透传（仅白名单内）',
        desc: '当某 cloudflared env 未被上面字段建模时的逃生舱。保留键无法覆盖。',
        example: { TUNNEL_DNS_RESOLVER_ADDRS: '1.1.1.1,1.0.0.1' },
        advanced: true,
      },
    ],
  },
];

/** advancedEnvOverrides 额外允许（不对应建模字段）的 env —— whitelist.go extraAllowed。 */
export const EXTRA_ALLOWED_ENV: { key: string; desc: string }[] = [
  { key: 'TUNNEL_DNS_RESOLVER_ADDRS', desc: '自定义 DNS 解析器列表（cloudflared 2025.7+）' },
  { key: 'TUNNEL_METRICS_UPDATE_FREQ', desc: '指标刷新间隔（仅展示用）' },
  { key: 'TUNNEL_MANAGEMENT_DIAGNOSTICS', desc: '经 CF 管理通道开启 /debug/pprof' },
];

/** cfdmgrd 自身注入、用户不可经 advancedEnvOverrides 覆盖的保留键 —— whitelist.go reservedOverride。 */
export const RESERVED_ENV: string[] = [
  'TUNNEL_TOKEN',
  'NO_AUTOUPDATE',
  'AUTOUPDATE_FREQ',
  'TUNNEL_METRICS',
  'TUNNEL_OUTPUT',
  'TUNNEL_LOGFILE',
  'TUNNEL_LOGDIRECTORY',
];

/** 由建模字段派生出的「白名单中可覆盖的 TUNNEL_* 键」。 */
export function modelledEnvKeys(): string[] {
  const out: string[] = [];
  for (const g of CATALOG) {
    for (const f of g.fields) {
      const e = f.env || '';
      const m = e.match(/^TUNNEL_[A-Z0-9_]+/);
      if (m) out.push(m[0]);
    }
  }
  return Array.from(new Set(out)).sort();
}

// setByPath 把 value 写入 root 的嵌套路径（如 "edge.protocol"）。
function setByPath(root: Record<string, unknown>, path: string, value: unknown): void {
  const parts = path.split('.');
  let cur = root;
  for (let i = 0; i < parts.length - 1; i++) {
    const k = parts[i];
    if (typeof cur[k] !== 'object' || cur[k] === null || Array.isArray(cur[k])) {
      cur[k] = {};
    }
    cur = cur[k] as Record<string, unknown>;
  }
  cur[parts[parts.length - 1]] = value;
}

// buildDraftYaml 按 CATALOG 顺序，把选中的字段路径拼成嵌套对象并渲染为 YAML 草稿。
// 顺序恒定（跟随目录而非勾选顺序），便于实时预览与复制粘贴。
export function buildDraftYaml(selected: string[]): string {
  const set = new Set(selected);
  const root: Record<string, unknown> = {};
  let any = false;
  for (const g of CATALOG) {
    for (const f of g.fields) {
      if (set.has(f.path)) {
        setByPath(root, f.path, f.example);
        any = true;
      }
    }
  }
  if (!any) return '';
  try {
    return stringifyYaml(root);
  } catch {
    return '';
  }
}

/** 把一个字段的示例值渲染成可直接粘贴的 YAML 片段。 */
export function fieldSnippet(group: CatalogGroup, f: FieldDef): string {
  const leaf = f.path.includes('.') ? f.path.split('.').slice(1).join('.') : f.path;
  const val = f.example;
  const renderScalar = (v: unknown): string => {
    if (typeof v === 'string') return v === '' ? '""' : v;
    return String(v);
  };
  if (val && typeof val === 'object' && !Array.isArray(val)) {
    // map 值：缩进展开
    const entries = Object.entries(val as Record<string, unknown>);
    if (!group.yamlKey) {
      return `${leaf}:\n${entries.map(([k, v]) => `  ${k}: ${renderScalar(v)}`).join('\n')}\n`;
    }
    return `${group.yamlKey}:\n  ${leaf}:\n${entries.map(([k, v]) => `    ${k}: ${renderScalar(v)}`).join('\n')}\n`;
  }
  if (!group.yamlKey) {
    return `${leaf}: ${renderScalar(val)}\n`;
  }
  return `${group.yamlKey}:\n  ${leaf}: ${renderScalar(val)}\n`;
}

/** 最小可用示例：只需 token。 */
export const MINIMAL_EXAMPLE = `# 最小配置：只需 token，其余字段用 cloudflared 默认
token: <粘贴你的 Cloudflare 隧道 token>
`;

/** 完整示例：覆盖全部参数，带说明注释。 */
export const FULL_EXAMPLE = `# ── cloudflared 隧道配置（token 模式）──
# token 必填（启动校验：100–1500 字符 base64）；其余留空=用 cloudflared 默认。
# ingress / public-hostname / origin 在 Cloudflare Zero Trust dashboard 配。
token: <粘贴你的 Cloudflare 隧道 token>

edge:                      # 与 Cloudflare 边缘的连接方式
  protocol: auto           # auto | http2 | quic（默认 auto）
  edgeIpVersion: "4"       # auto | 4 | 6（默认 4；注意是 edgeIpVersion）
  region: ""               # "" 全球 | us
  edgeBindAddress: ""      # 固定出站源 IP（高级，留空走 OS 默认）
  postQuantum: false       # 抗量子密钥交换，仅 protocol=quic 有效（高级）

reliability:
  retries: 5               # 连接/协议重试次数，1–20（默认 5）
  gracePeriod: 30s         # SIGTERM 后等待在途请求，1s–5m（默认 30s）

logging:
  logLevel: info           # debug | info | warn | error | fatal（默认 info）
  transportLogLevel: info  # 传输层(QUIC/HTTP2)日志级别（高级）

identity:
  label: my-connector      # 连接器显示名（Zero Trust 看板可见）
  tags:                    # 转发到看板的键值标注（高级）
    env: prod
    team: infra

binaryVersion: current     # current=跟随激活版本，或钉某 tag 如 2026.5.2

advancedEnvOverrides:      # 直接注入白名单内的 cloudflared env（高级逃生舱）
  TUNNEL_DNS_RESOLVER_ADDRS: 1.1.1.1,1.0.0.1
`;
