// instance 详情面板共享的小格式化工具。

/** 毫秒时长 → 人类可读（天/时/分/秒）。负数 / 非有限返回 —。 */
export function fmtDuration(ms: number): string {
  if (!isFinite(ms) || ms < 0) return '—';
  const s = Math.floor(ms / 1000);
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (d > 0) return `${d}天 ${h}时 ${m}分`;
  if (h > 0) return `${h}时 ${m}分`;
  if (m > 0) return `${m}分 ${sec}秒`;
  return `${sec}秒`;
}

/** 字节 → 人类可读（B/KB/MB/GB/TB）。 */
export function fmtBytes(n: number): string {
  if (!isFinite(n) || n <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`;
}

/** 整数千分位。 */
export function fmtNum(n: number | undefined): string {
  if (n === undefined || !isFinite(n)) return '0';
  return n.toLocaleString('en-US');
}
