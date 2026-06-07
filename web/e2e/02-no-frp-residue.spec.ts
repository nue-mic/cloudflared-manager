/**
 * 确认产品已彻底从 frps-manager 切到 cloudflared-manager，无 frp 残留：
 *   - 侧栏品牌是 "Cloudflared Manager"
 *   - 菜单含 cloudflared 专属项："cloudflared 实例" / "二进制管理" / "告警"
 *   - 侧栏导航区不出现 frps / frpc / FRPS / NAT 探测 等旧文案
 *   - 已删除的 /runtime/* 与 /nathole/* 端点返回 404
 *   - 仍存在的端点（/alerts、/metrics/{id}/traffic）可达
 *
 * 注意：负向文案检查**只**针对登录后的应用外壳（侧栏导航 nav 区域），
 * 不扫整页 body —— 登录页 Hero 目前仍残留 "FRPS Manager" 字样（属 web/src
 * 范畴，不在本 e2e 任务的修改范围内）。
 */
import { test, expect } from './fixtures/daemon';
import { login, sidebar, brand } from './helpers/selectors';

test('应用外壳呈现 cloudflared 文案/菜单，无 frp 残留', async ({ page, daemon }) => {
  // 登录
  await page.goto(daemon.baseURL);
  await login.tokenInput(page).fill(daemon.token);
  await login.submitBtn(page).click();
  await page.waitForLoadState('networkidle');

  // 品牌：Cloudflared Manager
  await expect(brand.sidebarTitle(page)).toBeVisible({ timeout: 10000 });

  // cloudflared 专属菜单项
  await expect(sidebar.instancesItem(page)).toBeVisible();
  await expect(sidebar.binariesItem(page)).toBeVisible();
  await expect(sidebar.alertsItem(page)).toBeVisible();

  // 侧栏导航区不应有 frps/frpc 残留菜单项
  await expect(page.getByRole('menuitem', { name: /FRPS 实例/i })).toHaveCount(0);
  await expect(page.getByRole('menuitem', { name: /FRPC 实例/i })).toHaveCount(0);
  await expect(page.getByRole('menuitem', { name: /NAT 探测/i })).toHaveCount(0);

  // 侧栏导航 nav 文本不含 frps/frpc 字样
  const navText = (await page.locator('aside .ant-menu').first().innerText()).toLowerCase();
  expect(navText).not.toContain('frps');
  expect(navText).not.toContain('frpc');
  expect(navText).toContain('cloudflared');
});

test('已删除的 frp 时代 API 端点返回 404', async ({ daemon }) => {
  const h = { Authorization: `Bearer ${daemon.token}` };

  // /runtime/* 已整组移除：GET 未匹配的 /api/* 落到 catch-all → http.NotFound → 404。
  const overview = await fetch(`${daemon.baseURL}/api/v1/runtime/anything/overview`, { headers: h });
  expect(overview.status).toBe(404);

  // 旧的 frpc 代理端点已不存在（GET 无此路由 → catch-all → 404）。
  const proxies = await fetch(`${daemon.baseURL}/api/v1/configs/anything/proxies`, { headers: h });
  expect(proxies.status).toBe(404);

  // /nathole/* 已整组移除。
  // GET 走 catch-all → 404；POST 因仅存 `GET /*` 通配路由而被 chi 判为 405。
  // 二者都证明专属端点已删除，故 GET 断言 404，POST 接受 [404,405]。
  const nhGet = await fetch(`${daemon.baseURL}/api/v1/nathole/discover`, { headers: h });
  expect(nhGet.status).toBe(404);
  const nhPost = await fetch(`${daemon.baseURL}/api/v1/nathole/discover`, {
    method: 'POST',
    headers: { ...h, 'Content-Type': 'application/json' },
    body: '{}',
  });
  expect([404, 405]).toContain(nhPost.status);

  // ---- 现存端点应可达（确认不是把整个 API 都 404 了）----

  // 历史流量曲线端点存在：路由命中后由 handler 处理（不会落到 catch-all 404）。
  // id 不存在 → 200 空序列；metrics 库未启用 → 503。两者都表明路由在。
  const traffic = await fetch(
    `${daemon.baseURL}/api/v1/metrics/anything/traffic?to=9999999999`,
    { headers: h },
  );
  expect([200, 503]).toContain(traffic.status);

  // 告警列表端点存在：200（库就绪）或 503（库被禁用），均非 catch-all 404。
  const alerts = await fetch(`${daemon.baseURL}/api/v1/alerts`, { headers: h });
  expect([200, 503]).toContain(alerts.status);

  // 健康探针无需鉴权即 200。
  const health = await fetch(`${daemon.baseURL}/api/v1/health`);
  expect(health.status).toBe(200);
});
