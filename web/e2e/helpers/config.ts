import { stringify as stringifyYaml } from 'yaml';

/**
 * cloudflared 隧道配置（TunnelConfigV1）的测试构造器。
 *
 * 关键事实（来自 pkg/cfdconfig）：
 *   - 字段全部 camelCase（edge / reliability / logging / identity / advancedEnvOverrides）。
 *   - token 由独立字段管理，长度需落在 [100,1500] 且仅含 base64 字符
 *     (`[A-Za-z0-9_\-+/=]+`) 才能通过 Validate 真正 start。create/store 不强制。
 *   - 后端 decodeJSON 开启 DisallowUnknownFields：多发一个键会 400，
 *     因此这里只产出后端确实认识的字段。
 */
export interface TunnelConfigV1 {
  token?: string;
  edge?: {
    protocol?: 'auto' | 'http2' | 'quic';
    edgeIpVersion?: 'auto' | '4' | '6';
    edgeBindAddress?: string;
    region?: '' | 'us';
    postQuantum?: boolean;
  };
  reliability?: {
    retries?: number;
    gracePeriod?: string;
  };
  logging?: {
    logLevel?: 'debug' | 'info' | 'warn' | 'error' | 'fatal';
    transportLogLevel?: 'debug' | 'info' | 'warn' | 'error' | 'fatal';
  };
  identity?: {
    label?: string;
    tags?: Record<string, string>;
  };
  advancedEnvOverrides?: Record<string, string>;
  binaryVersion?: string;
}

/**
 * 生成一个合法的 120 字符“假” base64 token。
 * 满足 Validate 的 charset + 长度约束 [100,1500]，因此 start 不会因 token
 * 校验而被拒；它在 CI 里只会因为没有真实 cloudflared 二进制 / 连不上 edge
 * 而最终落到 last_error，这正是测试想验证的「start 返回 Snapshot，即便随后出错」。
 */
export function dummyToken(len = 120): string {
  // 'A' 属于 base64 字母表，重复即可满足正则与长度。
  return 'A'.repeat(Math.max(100, Math.min(1500, len)));
}

/**
 * 最小可用 cloudflared 配置（API 创建 payload 的 `config` 字段）。
 * 默认带一个合法 dummy token，便于直接走 start 流程。传 withToken=false
 * 可产出无 token 的草稿（create 仍成功，但 start 会因缺 token 失败）。
 */
export function minimalTunnelConfig(opts: { withToken?: boolean } = {}): TunnelConfigV1 {
  const cfg: TunnelConfigV1 = {
    edge: { protocol: 'auto' },
    logging: { logLevel: 'info' },
  };
  if (opts.withToken !== false) {
    cfg.token = dummyToken();
  }
  return cfg;
}

/**
 * 把一份配置序列化为 cloudflared YAML 文本（PutRaw / YAML 编辑器场景用）。
 * token 永远从 YAML 中剔除——它由独立密码字段管理（与前端 Configs.tsx 一致）。
 */
export function configToYaml(cfg: TunnelConfigV1): string {
  const rest: Record<string, unknown> = { ...(cfg as Record<string, unknown>) };
  delete rest.token;
  const text = stringifyYaml(rest);
  return text.trim() === '{}' ? '' : text;
}
