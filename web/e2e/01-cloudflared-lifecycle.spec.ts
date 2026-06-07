/**
 * cloudflared 隧道实例生命周期端到端：
 *   登录 → 进入 cloudflared 实例页 → API 建配置 → UI 看到卡片 →
 *   API 启动（断言返回 Snapshot，**不**假定真起来）→ 编辑名称 →
 *   删除 → 卡片消失。
 *
 * 设计取舍：用 REST helper 做 setup（绕开 UI 复杂 Modal），UI 只验证
 * 「前端正确显示后端真实状态」。CI 里没有真实 cloudflared 二进制，所以
 * POST /start 会因 cmd.Start 失败而返回 400（spawn cloudflared）；若环境恰好
 * 装了 cloudflared 则返回 200 + Snapshot。本测试对两种结果都成立，只验证
 * /start 路由有效且实例最终落到 stopped —— 不要求隧道真正 started。
 */
import { test, expect } from './fixtures/daemon';
import { api } from './helpers/api';
import { login, sidebar, configList } from './helpers/selectors';
import { minimalTunnelConfig } from './helpers/config';

test('登录 → 建/启/编辑/删 cloudflared 隧道配置（端到端）', async ({ page, daemon }) => {
  const a = api(daemon);
  const id = 'e2e1';

  // ---- 登录 ----
  await page.goto(daemon.baseURL);
  await login.tokenInput(page).fill(daemon.token);
  await login.submitBtn(page).click();

  // 登录成功后跳到仪表盘，侧栏菜单可见
  await expect(sidebar.instancesItem(page)).toBeVisible({ timeout: 10000 });

  // 跳到 cloudflared 实例页
  await sidebar.instancesItem(page).click();
  await expect(configList.heading(page)).toBeVisible({ timeout: 10000 });

  // ---- 用 API 建一个带合法 dummy token 的配置 ----
  const created = await a.createConfig(id, '端到端测试 1', minimalTunnelConfig());
  expect(created.id).toBe(id);
  expect(created.cfdmgr.name).toBe('端到端测试 1');
  // 信封绝不回传明文 token，但 has_token 应为 true
  expect(created.has_token).toBe(true);
  expect(created.config?.token ?? '').toBe('');

  // 列表 API 也应能看到它
  const list = await a.listConfigs();
  expect(list.map((c) => c.id)).toContain(id);

  // ---- UI 应（轮询 / WS 刷新后）看到卡片 ----
  // Configs 页每 4s 轮询一次，必要时 reload 强制取最新列表
  await page.reload();
  await expect(configList.configCard(page, id)).toBeVisible({ timeout: 10000 });

  // ---- 启动：端点已接线，但不假定隧道真起来 ----
  // CI 默认无 cloudflared 二进制：cmd.Start 立刻失败 → 后端 400（spawn cloudflared）。
  // 若环境恰好装了 cloudflared：返回 200 + Snapshot（随后多半因连不上 edge 而退出）。
  // 两种结果都证明 /start 路由有效（绝非 404），这才是本步骤要验证的。
  const startRes = await a.startRaw(id);
  expect([200, 400, 409]).toContain(startRes.status);
  if (startRes.status === 200) {
    const snap = startRes.body as { id: string; state: string };
    expect(snap.id).toBe(id);
    expect(['starting', 'started', 'stopping', 'stopped']).toContain(snap.state);
  }

  // 无论上面是哪条路径，实例最终都应停在 stopped（启动失败或进程退出）。
  const settled = await a.waitForState(id, ['stopped', 'started'], 10000);
  expect(settled.id).toBe(id);

  // 停止幂等（即便已 stopped 也返回 200 Snapshot）
  const stopSnap = await a.stop(id);
  expect(stopSnap.state).toBe('stopped');

  // ---- 编辑：改显示名，UI 回读应反映 ----
  const editResp = await fetch(`${daemon.baseURL}/api/v1/configs/${id}`, {
    method: 'PUT',
    headers: { Authorization: `Bearer ${daemon.token}`, 'Content-Type': 'application/json' },
    // token 留空 = 保留现有；只改 cfdmgr.name
    body: JSON.stringify({ config: minimalTunnelConfig({ withToken: false }), cfdmgr: { name: '改名后的隧道', manualStart: true } }),
  });
  expect(editResp.ok).toBe(true);
  const afterEdit = await a.getConfig(id);
  expect(afterEdit.cfdmgr.name).toBe('改名后的隧道');
  // token 留空提交不应抹掉原 token
  expect(afterEdit.has_token).toBe(true);

  // UI reload 后卡片仍在，且显示新名字
  await page.reload();
  const card = configList.configCard(page, id);
  await expect(card).toBeVisible({ timeout: 10000 });
  await expect(card.getByText('改名后的隧道')).toBeVisible();

  // ---- 删除 → 卡片消失 ----
  await a.deleteConfig(id);
  await expect(a.listConfigs()).resolves.toEqual([]);
  await page.reload();
  await expect(configList.configCard(page, id)).toHaveCount(0, { timeout: 10000 });
});
