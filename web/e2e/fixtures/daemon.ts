import { test as base } from '@playwright/test';
import { spawn, ChildProcess } from 'node:child_process';
import { mkdtempSync, rmSync, existsSync, createWriteStream, readFileSync } from 'node:fs';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

export type Daemon = {
  /** 完整 base URL, 例: http://127.0.0.1:18080 */
  baseURL: string;
  /** API token */
  token: string;
  /** TempDir 绝对路径（成功时 cleanup, 失败时保留） */
  dataDir: string;
  /** daemon.log 文件路径 */
  logPath: string;
};

type Fixtures = {
  daemon: Daemon;
};

/**
 * 拓展 Playwright base test, 注入 `daemon` fixture (worker scope).
 *
 * 每个 worker 启动时:
 *   1. 创建独立 TempDir
 *   2. 起一个 cfdmgrd 子进程, 监听 127.0.0.1:18080+workerIndex
 *   3. 轮询 GET /api/v1/health 直到 200 (max 5s)
 *   4. 测试运行
 *   5. 结束时 kill daemon, 全绿就删 TempDir, 否则保留
 *
 * 注意：cfdmgrd 在缺少 CFDM_API_TOKEN 时直接拒绝启动，因此 token 必填。
 */
export const test = base.extend<{}, Fixtures>({
  daemon: [
    async ({}, use, workerInfo) => {
      const bin = process.env.CFDMGRD_BIN;
      if (!bin || !existsSync(bin)) {
        throw new Error(
          `CFDMGRD_BIN not set or not exists (${bin}). globalSetup should have set it.`,
        );
      }

      const port = 18080 + workerInfo.workerIndex;
      const token = `e2e-token-${workerInfo.workerIndex}`;
      const e2eTmpRoot = resolve(__dirname, '..', '..', 'e2e-tmp');
      const dataDir = mkdtempSync(join(e2eTmpRoot, `w${workerInfo.workerIndex}-`));
      const logPath = join(dataDir, 'daemon.log');
      const logStream = createWriteStream(logPath, { flags: 'a' });

      const proc: ChildProcess = spawn(bin, ['serve'], {
        env: {
          ...process.env,
          CFDM_API_TOKEN: token,
          CFDM_HTTP_ADDR: `127.0.0.1:${port}`,
          CFDM_DATA_DIR: dataDir,
          CFDM_LOG_LEVEL: 'info',
        },
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      proc.stdout?.pipe(logStream);
      proc.stderr?.pipe(logStream);

      const baseURL = `http://127.0.0.1:${port}`;
      const ready = await waitForReady(baseURL, 5000);
      if (!ready) {
        proc.kill('SIGKILL');
        logStream.end();
        throw new Error(
          `daemon did not become ready in 5s on ${baseURL}. daemon log tail:\n${tailFile(logPath, 50)}`,
        );
      }

      let testFailed = false;
      try {
        await use({ baseURL, token, dataDir, logPath });
      } catch (e) {
        testFailed = true;
        throw e;
      } finally {
        proc.kill('SIGTERM');
        await delay(500);
        if (proc.exitCode == null) proc.kill('SIGKILL');
        logStream.end();
        if (!testFailed) {
          rmSync(dataDir, { recursive: true, force: true });
        } else {
          // eslint-disable-next-line no-console
          console.log(`[daemon fixture] preserving ${dataDir} for inspection`);
        }
      }
    },
    { scope: 'worker' },
  ],
});

/**
 * 轮询 /api/v1/health（**无需鉴权**）直到 200 或超时。
 * health 探针不在 Bearer 子树内，所以这里不带 Authorization。
 */
async function waitForReady(baseURL: string, timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const r = await fetch(`${baseURL}/api/v1/health`);
      if (r.ok) return true;
    } catch {
      // ignore, retry
    }
    await delay(100);
  }
  return false;
}

function delay(ms: number): Promise<void> {
  return new Promise((res) => setTimeout(res, ms));
}

function tailFile(path: string, lines: number): string {
  try {
    const data = readFileSync(path, 'utf8');
    return data.split(/\r?\n/).slice(-lines).join('\n');
  } catch {
    return '(no log)';
  }
}

export { expect } from '@playwright/test';
