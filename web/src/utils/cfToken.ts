// cloudflared connector token 提取。
//
// 用户常常直接把 Cloudflare 控制台给的整条安装命令贴进来，例如：
//   cloudflared.exe service install eyJhIjoi...
//   cloudflared tunnel run --token eyJhIjoi...
//   sudo cloudflared service install eyJhIjoi...
//   docker run cloudflare/cloudflared:latest tunnel --no-autoupdate run --token eyJhIjoi...
// 这些都不是裸 token，后端按 base64 校验会因空格/命令词报
// "token: contains non-base64 characters"。
//
// cloudflared 的 connector token 是 base64(JSON)，JSON 以 {"a": 开头，
// base64 后必然以 "eyJ" 开头，字符集为 base64（标准 + url-safe + 填充）。
// 故只需在输入中抓出第一段 eyJ 开头的 base64 串即可，遇空白即停。

const TOKEN_RE = /eyJ[A-Za-z0-9_\-+/=]+/;

/**
 * 从任意粘贴文本中提取裸 cloudflared token。
 * 找不到 eyJ 段时回退为 trim 后的原文（让后端做最终校验/报错）。
 */
export function extractCloudflaredToken(input: string): string {
  if (!input) return '';
  const m = input.match(TOKEN_RE);
  if (m) return m[0];
  return input.trim();
}
