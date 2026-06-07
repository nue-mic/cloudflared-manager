import type { Page, Locator } from '@playwright/test';

/**
 * 集中维护的 UI 选择器。所有 spec 只从这里取 Locator，不在 spec 内写裸 CSS/XPath。
 *
 * 真实 UI 事实（来自 web/src）：
 *   - 品牌：侧栏顶部 "Cloudflared Manager"（MainLayout.tsx）。
 *   - 实例菜单项：'cloudflared 实例'（路由 /configs）；二进制 '二进制管理'。
 *   - Configs 页 **没有** data-testid，实例以 List+Card 渲染，卡片内含
 *     "ID: {id}" 文本与显示名；右上角有 "新建" 按钮。
 *   - 编辑/新建 Modal 含 "Cloudflared Token" 字段 + YAML 编辑器。
 *   - 登录页：密码框 placeholder "API Token (Bearer 令牌)"，按钮 "验证并进入控制台"。
 */

export const login = {
  tokenInput: (p: Page): Locator => p.getByPlaceholder(/API Token|Bearer/i),
  submitBtn: (p: Page): Locator =>
    p.getByRole('button', { name: /验证并进入控制台|登录|login|sign in/i }),
  errorMsg: (p: Page): Locator => p.getByText(/失败|invalid|无效|failed/i),
};

export const brand = {
  // 侧栏品牌文案
  sidebarTitle: (p: Page): Locator => p.getByText('Cloudflared Manager', { exact: true }),
};

export const sidebar = {
  // 实例菜单项：'cloudflared 实例'
  instancesItem: (p: Page): Locator =>
    p.getByRole('menuitem', { name: /cloudflared 实例|实例/i }),
  binariesItem: (p: Page): Locator =>
    p.getByRole('menuitem', { name: /二进制管理/ }),
  dashboardItem: (p: Page): Locator =>
    p.getByRole('menuitem', { name: /仪表盘|dashboard/i }),
  alertsItem: (p: Page): Locator => p.getByRole('menuitem', { name: /告警/ }),
};

export const configList = {
  // 新建按钮（隧道实例标题旁）
  createBtn: (p: Page): Locator => p.getByRole('button', { name: /新建/ }),
  // 隧道实例分栏标题
  heading: (p: Page): Locator => p.getByRole('heading', { name: /隧道实例/ }),
  /**
   * 定位某实例卡片：卡片内渲染 "ID: {id}" 文本，向上回溯到 .ant-card。
   * 没有 data-testid，所以靠 ID 文本锚定。
   */
  configCard: (p: Page, id: string): Locator =>
    p.locator('.ant-card', { has: p.getByText(`ID: ${id}`, { exact: true }) }),
  // 卡片内的状态徽章文案
  statusText: (card: Locator): Locator =>
    card.getByText(/运行中|已停止|启动中|停止中/).first(),
};

export const editModal = {
  // 弹窗内 "Cloudflared Token" 密码字段
  tokenInput: (p: Page): Locator =>
    p.getByRole('dialog').locator('input[type="password"]').first(),
  // 显示名输入框（placeholder "例如: 生产隧道"）
  nameInput: (p: Page): Locator => p.getByPlaceholder(/生产隧道|显示名/),
  // 唯一 ID 输入框（仅新建时出现，placeholder "例如: my-tunnel"）
  idInput: (p: Page): Locator => p.getByPlaceholder(/my-tunnel/),
  saveBtn: (p: Page): Locator => p.getByRole('button', { name: /保存/ }),
};
