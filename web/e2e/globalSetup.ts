import { existsSync, mkdirSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

/**
 * Playwright globalSetup — 在所有 worker 启动前调用一次。
 *
 * 职责：
 *   1. 找到 bin/cfdmgrd[.exe]（或 bin/cfdmgrd-dev[.exe]），塞到 CFDMGRD_BIN env var
 *   2. ensure web/e2e-tmp/ 目录存在（mkdtempSync 要求父目录存在）
 *
 * 不在职责内：
 *   - 主动构建 daemon（避免每次跑测都触发昂贵的 Go 编译）
 *   - 启动 daemon（那是每个 spec 的 daemon fixture 干的事）
 *
 * daemon 二进制由如下命令构建（cloudflared-manager）：
 *   go build -o bin/cfdmgrd ./cmd/cfdmgrd        # Linux/macOS
 *   go build -o bin/cfdmgrd.exe ./cmd/cfdmgrd    # Windows
 */
export default async function globalSetup() {
  const projectRoot = resolve(__dirname, '..', '..');
  const isWin = process.platform === 'win32';
  const exe = isWin ? '.exe' : '';
  const candidates = [
    resolve(projectRoot, 'bin', `cfdmgrd-dev${exe}`),
    resolve(projectRoot, 'bin', `cfdmgrd${exe}`),
  ];
  const found = candidates.find((p) => existsSync(p));
  if (!found) {
    throw new Error(
      `cfdmgrd binary not found at any of:\n  ${candidates.join('\n  ')}\n` +
        `Build it first:\n` +
        `  go build -o bin/cfdmgrd${exe} ./cmd/cfdmgrd\n` +
        `(or \`make build-host\`, which also rebuilds the embedded web/dist).`,
    );
  }
  process.env.CFDMGRD_BIN = found;

  const e2eTmp = resolve(__dirname, '..', 'e2e-tmp');
  mkdirSync(e2eTmp, { recursive: true });

  // eslint-disable-next-line no-console
  console.log(`[globalSetup] cfdmgrd binary: ${found}`);
}
