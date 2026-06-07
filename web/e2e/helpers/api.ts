import type { Daemon } from '../fixtures/daemon';
import { minimalTunnelConfig, type TunnelConfigV1 } from './config';

/** manager 元数据信封（请求体顶层 "cfdmgr" 字段）。 */
export interface MgrMeta {
  name: string;
  manualStart: boolean;
}

/** 实例运行态快照（snake_case，见 internal/manager/instance.go Snapshot）。 */
export interface Snapshot {
  id: string;
  name: string;
  path: string;
  log_path: string;
  state: 'stopped' | 'starting' | 'started' | 'stopping' | 'unknown';
  last_error?: string;
  started_at?: string;
  stopped_at?: string;
  binary_version?: string;
  pid?: number;
  metrics_port?: number;
}

/** ConfigEnvelope = Snapshot + config(token 已脱敏) + cfdmgr + has_token。 */
export interface ConfigEnvelope extends Snapshot {
  config: TunnelConfigV1 | null;
  cfdmgr: MgrMeta;
  has_token: boolean;
}

/**
 * 直接调 daemon REST API 的 helper. 用于在测试中快速 setup 状态
 * (绕过 UI 加速, UI 自己的交互由 spec 内的 page actions 测).
 *
 * 创建配置的请求体形如：
 *   { id, config: TunnelConfigV1, cfdmgr: { name, manualStart } }
 * 信封键是 "cfdmgr"（不是旧的 "frpsmgr"）。
 */
export function api(daemon: Daemon) {
  const h = { Authorization: `Bearer ${daemon.token}`, 'Content-Type': 'application/json' };

  return {
    /**
     * 创建一个 cloudflared 隧道配置。
     * @param id          唯一 ID（文件 stem）
     * @param name        显示名（默认等于 id）
     * @param config      TunnelConfigV1；默认带一个合法 dummy token 的最小配置
     * @param manualStart 默认 true，避免 fixture daemon 启动时被 AutoStart 自动拉起
     */
    async createConfig(
      id: string,
      name = id,
      config: TunnelConfigV1 = minimalTunnelConfig(),
      manualStart = true,
    ): Promise<ConfigEnvelope> {
      const r = await fetch(`${daemon.baseURL}/api/v1/configs`, {
        method: 'POST',
        headers: h,
        body: JSON.stringify({ id, config, cfdmgr: { name, manualStart } }),
      });
      if (!r.ok) throw new Error(`createConfig(${id}) failed: ${r.status} ${await r.text()}`);
      return (await r.json()) as ConfigEnvelope;
    },

    /** GET /configs → 完整列表（items 为 Snapshot[]）。 */
    async listConfigs(): Promise<Snapshot[]> {
      const r = await fetch(`${daemon.baseURL}/api/v1/configs`, { headers: h });
      if (!r.ok) throw new Error(`listConfigs failed: ${r.status}`);
      const body = (await r.json()) as { items?: Snapshot[] };
      return body.items ?? [];
    },

    /** GET /configs/{id} → ConfigEnvelope（config.token 已脱敏，永不返回明文）。 */
    async getConfig(id: string): Promise<ConfigEnvelope> {
      const r = await fetch(`${daemon.baseURL}/api/v1/configs/${id}`, { headers: h });
      if (!r.ok) throw new Error(`getConfig(${id}) failed: ${r.status}`);
      return (await r.json()) as ConfigEnvelope;
    },

    /**
     * POST /configs/{id}/start → 200 Snapshot。
     * 注意：CI 无真实 cloudflared 二进制时，实例会进入 starting/stopped 并带
     * last_error；本调用只要 HTTP 2xx 即视为「指令被接受」，不假定真起来。
     */
    async start(id: string): Promise<Snapshot> {
      const r = await fetch(`${daemon.baseURL}/api/v1/configs/${id}/start`, {
        method: 'POST',
        headers: h,
      });
      if (!r.ok) throw new Error(`start(${id}) failed: ${r.status} ${await r.text()}`);
      return (await r.json()) as Snapshot;
    },

    /**
     * 像 start() 但**不**在非 2xx 时抛异常，返回 { status, body }。
     * 用于断言「端点已接受请求」而不假定结果：
     *   - 有真实 cloudflared 二进制 → 200 + Snapshot
     *   - CI 无二进制 → 400（spawn cloudflared 失败，writeManagerError 落到 default）
     * 两者都证明 /start 路由已正确接线（绝不是 404/路由缺失）。
     */
    async startRaw(id: string): Promise<{ status: number; body: unknown }> {
      const r = await fetch(`${daemon.baseURL}/api/v1/configs/${id}/start`, {
        method: 'POST',
        headers: h,
      });
      let body: unknown = null;
      try {
        body = await r.json();
      } catch {
        body = null;
      }
      return { status: r.status, body };
    },

    async stop(id: string): Promise<Snapshot> {
      const r = await fetch(`${daemon.baseURL}/api/v1/configs/${id}/stop`, {
        method: 'POST',
        headers: h,
      });
      if (!r.ok) throw new Error(`stop(${id}) failed: ${r.status} ${await r.text()}`);
      return (await r.json()) as Snapshot;
    },

    async reload(id: string): Promise<Snapshot> {
      const r = await fetch(`${daemon.baseURL}/api/v1/configs/${id}/reload`, {
        method: 'POST',
        headers: h,
      });
      if (!r.ok) throw new Error(`reload(${id}) failed: ${r.status} ${await r.text()}`);
      return (await r.json()) as Snapshot;
    },

    /** GET /configs/{id}/status → Snapshot。 */
    async getStatus(id: string): Promise<Snapshot> {
      const r = await fetch(`${daemon.baseURL}/api/v1/configs/${id}/status`, { headers: h });
      if (!r.ok) throw new Error(`getStatus(${id}) failed: ${r.status}`);
      return (await r.json()) as Snapshot;
    },

    async deleteConfig(id: string): Promise<void> {
      const r = await fetch(`${daemon.baseURL}/api/v1/configs/${id}`, {
        method: 'DELETE',
        headers: h,
      });
      if (!r.ok && r.status !== 404) {
        throw new Error(`deleteConfig(${id}) failed: ${r.status}`);
      }
    },

    /** 轮询 status 直到 state 命中 wanted 集合之一，或超时。 */
    async waitForState(id: string, wanted: Snapshot['state'][], timeoutMs = 10000): Promise<Snapshot> {
      const deadline = Date.now() + timeoutMs;
      let last: Snapshot | null = null;
      while (Date.now() < deadline) {
        try {
          last = await this.getStatus(id);
          if (wanted.includes(last.state)) return last;
        } catch {
          // ignore, retry
        }
        await new Promise((res) => setTimeout(res, 250));
      }
      throw new Error(
        `waitForState(${id}, [${wanted.join('|')}]) timed out; last=${JSON.stringify(last)}`,
      );
    },
  };
}
