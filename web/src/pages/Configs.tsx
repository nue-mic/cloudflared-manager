import { useEffect, useState, useRef, useMemo } from 'react';
import {
  Card, Row, Col, Button, Badge, Space, Typography, Popconfirm,
  Form, Input, Switch, Modal, message, Tooltip, Empty, List,
  theme as antdTheme,
} from 'antd';
import {
  PlayCircleOutlined,
  StopOutlined,
  ReloadOutlined,
  DeleteOutlined,
  CopyOutlined,
  EditOutlined,
  PlusOutlined,
  ExclamationCircleOutlined,
  HolderOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
} from '@ant-design/icons';

import CodeMirror from '@uiw/react-codemirror';
import { StreamLanguage } from '@codemirror/language';
import { yaml as yamlMode } from '@codemirror/legacy-modes/mode/yaml';
import { oneDark } from '@codemirror/theme-one-dark';
import { EditorView } from '@codemirror/view';
import { parse as parseYaml, stringify as stringifyYaml } from 'yaml';

import client from '../api/client';
import { useTheme } from '../theme/ThemeContext';
import { useEventSubscription } from '../events/EventStreamContext';
import type { InstanceStateData } from '../events/types';
import type { Snapshot, ConfigEnvelope, MgrMeta, TunnelConfigV1 } from '../api/types';
import InstanceDetailPanel from '../components/instance/InstanceDetailPanel';
import { extractCloudflaredToken } from '../utils/cfToken';

const { Title, Text } = Typography;

const VSCODE_MONO = `'Cascadia Code', Consolas, 'SF Mono', Menlo, monospace`;
const yamlFontTheme = EditorView.theme({
  '&': { fontFamily: VSCODE_MONO, fontSize: '13.5px' },
  '.cm-content': { fontFamily: VSCODE_MONO },
  '.cm-gutters': { fontFamily: VSCODE_MONO, fontSize: '12.5px' },
  '.cm-scroller': { lineHeight: '1.55' },
});

// 用真正的 YAML 库做双向转换。cloudflared 配置是嵌套结构
// (edge/reliability/logging/identity)，旧的扁平解析会把嵌套字段拍平丢失，
// 导致保存时发出未知顶层键被后端 DisallowUnknownFields 拒绝(400)。
//
// token 永不进入 YAML 编辑器——它由独立的密码字段管理(留空=保持现有)，
// 后端也已在响应里脱敏。
function configToYaml(cfg: TunnelConfigV1): string {
  const rest: Record<string, unknown> = { ...(cfg as Record<string, unknown>) };
  delete rest.token;
  try {
    const text = stringifyYaml(rest);
    return text.trim() === '{}' ? '' : text;
  } catch {
    return '';
  }
}

// 编辑器顶部的「字段 + 官方支持取值」参考注释。纯 YAML 注释，保存时被 parseYaml
// 自然丢弃（零副作用），仅作编辑/新建时的速查与复制粘贴。取值口径与后端
// pkg/cfdconfig 校验逐字一致（validProtocols / validEdgeIPVersions / validRegions /
// validLogLevels / retries[0,20] / gracePeriod[1s,5m]）。
const YAML_HINT = `# ── cloudflared 隧道配置参考（token 请填上方字段，勿写在此处）──
# 仅 token 模式：ingress / 公共主机名 / origin 在「Cloudflare」菜单或官方后台管理。
# 以下为全部可选字段与官方支持取值，可按需复制粘贴；留空即用 cloudflared 默认值。
#
# edge:                       # 与 Cloudflare 边缘的连接方式
#   protocol: auto            # 协议类型。可选: auto(默认,优先 QUIC,失败回退 http2) | quic | http2
#   edgeIpVersion: auto       # 边缘连接 IP 版本。可选: auto | 4(默认) | 6
#   edgeBindAddress: ""       # 出口本地 IP(可选;其 IP 族会覆盖 edgeIpVersion)
#   region: ""                # 边缘区域。可选: ""(全球,默认) | us
#   postQuantum: false        # 后量子密钥交换(仅 protocol=quic 有效)。可选: true | false
# reliability:
#   retries: 0                # 连接重试上限。范围 0..20(0=用 cloudflared 默认值 5)
#   gracePeriod: 30s          # 停止前等待在途请求的时长。范围 1s..5m(如 30s / 2m)
# logging:
#   logLevel: info            # 应用日志级别。可选: debug | info(默认) | warn | error | fatal
#   transportLogLevel: info   # 传输层(QUIC/HTTP2)日志级别。取值同 logLevel
# identity:
#   label: ""                 # 连接器显示名(回传 CF 后台)。≤64,字符集 字母/数字/_/-/./空格
#   tags:                     # 连接器标签(回传 CF 后台),key 须匹配 [A-Za-z_][A-Za-z0-9_]*
#     env: prod
# binaryVersion: ""           # 钉 cloudflared 版本。可选: ""/current(跟随全局) | 具体版本如 2026.5.2
# advancedEnvOverrides:       # 高级 TUNNEL_* 覆盖(白名单;禁覆盖 token/metrics 等保留键)
#   TUNNEL_XXX: value
#
`;

// ─────────────────────────────────────────────────────────────────────────────

interface EditFormValues {
  id: string;
  name: string;
  token: string;
  manualStart: boolean;
}

const Configs: React.FC = () => {
  const { token: themeToken } = antdTheme.useToken();
  const { resolved: themeMode } = useTheme();
  const yamlExtensions = [StreamLanguage.define(yamlMode), yamlFontTheme];

  const [configs, setConfigs] = useState<Snapshot[]>([]);
  const [statusLoading, setStatusLoading] = useState<Record<string, boolean>>({});
  const [activeConfigId, setActiveConfigId] = useState<string>('');

  // 编辑弹窗
  const [modalOpen, setModalOpen] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null); // null = 新建
  const [yamlText, setYamlText] = useState('');
  const [saving, setSaving] = useState(false);
  const [hasToken, setHasToken] = useState(false); // 编辑时：该实例是否已存储 token

  // 复制弹窗
  const [dupModalOpen, setDupModalOpen] = useState(false);
  const [dupSourceId, setDupSourceId] = useState('');
  const [dupNewId, setDupNewId] = useState('');

  const [form] = Form.useForm<EditFormValues>();

  // 拖动排序 + 列表缩窄
  const [dragId, setDragId] = useState<string | null>(null);
  const [overId, setOverId] = useState<string | null>(null);
  const [listCollapsed, setListCollapsed] = useState<boolean>(
    () => localStorage.getItem('cfdmgr_inst_list_collapsed') === '1'
  );

  const toggleListCollapsed = () => {
    setListCollapsed((v) => {
      const next = !v;
      localStorage.setItem('cfdmgr_inst_list_collapsed', next ? '1' : '0');
      return next;
    });
  };

  const configsRef = useRef(configs);
  useEffect(() => { configsRef.current = configs; }, [configs]);

  useEffect(() => { fetchConfigs(); }, []);

  // 轮询状态
  useEffect(() => {
    const poll = () => {
      configsRef.current.forEach((c) => fetchStatus(c.id));
    };
    poll();
    const timer = setInterval(poll, 4000);
    return () => clearInterval(timer);
  }, []);

  // WebSocket 事件驱动刷新
  useEventSubscription(['config.changed', 'config.deleted', 'instance.state'], (e) => {
    if (e.type === 'instance.state' && e.config_id) {
      const st = (e.data as InstanceStateData | undefined)?.state;
      if (st) {
        setConfigs((prev) =>
          prev.map((c) => (c.id === e.config_id ? { ...c, state: st as Snapshot['state'] } : c))
        );
      }
    } else if (e.type === 'config.deleted' && e.config_id) {
      setConfigs((prev) => prev.filter((c) => c.id !== e.config_id));
      setActiveConfigId((prev) => (prev === e.config_id ? '' : prev));
    } else if (e.type === 'config.changed') {
      fetchConfigs();
    }
  });

  const fetchConfigs = async () => {
    try {
      const resp = await client.get('/api/v1/configs');
      if (resp.status === 200) {
        const items: Snapshot[] = resp.data?.items || [];
        setConfigs(items);
        if (items.length > 0 && !activeConfigId) {
          setActiveConfigId(items[0].id);
        }
      }
    } catch {
      message.error('无法获取配置列表');
    }
  };

  // 持久化新顺序到后端（meta.json Sort；备份/还原会带上此顺序）。
  const persistOrder = async (order: string[]) => {
    try {
      await client.post('/api/v1/configs/reorder', { order });
    } catch {
      message.error('保存排序失败，已回滚');
      fetchConfigs();
    }
  };

  // 把 dragId 拖到 targetId 的位置，乐观更新 + 落盘。
  const handleReorderDrop = (targetId: string) => {
    const src = dragId;
    setDragId(null);
    setOverId(null);
    if (!src || src === targetId) return;
    setConfigs((prev) => {
      const from = prev.findIndex((c) => c.id === src);
      const to = prev.findIndex((c) => c.id === targetId);
      if (from < 0 || to < 0) return prev;
      const next = [...prev];
      const [moved] = next.splice(from, 1);
      next.splice(to, 0, moved);
      persistOrder(next.map((c) => c.id));
      return next;
    });
  };

  const fetchStatus = async (id: string) => {
    try {
      const resp = await client.get(`/api/v1/configs/${id}/status`);
      if (resp.status === 200) {
        const snap = resp.data as Snapshot;
        setConfigs((prev) => prev.map((c) => (c.id === id ? { ...c, ...snap } : c)));
      }
    } catch {/* 静默 */}
  };

  // ── 生命周期操作 ──────────────────────────────────────────────────────────

  const handleStart = async (id: string) => {
    setStatusLoading((p) => ({ ...p, [id]: true }));
    try {
      await client.post(`/api/v1/configs/${id}/start`);
      message.success('启动指令已发送');
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
      message.error('启动失败: ' + (e.response?.data?.error?.message || e.message));
    } finally {
      setStatusLoading((p) => ({ ...p, [id]: false }));
      fetchStatus(id);
    }
  };

  const handleStop = async (id: string) => {
    setStatusLoading((p) => ({ ...p, [id]: true }));
    try {
      await client.post(`/api/v1/configs/${id}/stop`);
      message.success('停止指令已发送');
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
      message.error('停止失败: ' + (e.response?.data?.error?.message || e.message));
    } finally {
      setStatusLoading((p) => ({ ...p, [id]: false }));
      fetchStatus(id);
    }
  };

  const handleReload = async (id: string) => {
    setStatusLoading((p) => ({ ...p, [id]: true }));
    try {
      await client.post(`/api/v1/configs/${id}/reload`);
      message.success('已重启实例');
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
      message.error('重启失败: ' + (e.response?.data?.error?.message || e.message));
    } finally {
      setStatusLoading((p) => ({ ...p, [id]: false }));
      fetchStatus(id);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await client.delete(`/api/v1/configs/${id}`);
      message.success('配置已删除');
      setConfigs((prev) => prev.filter((c) => c.id !== id));
      if (activeConfigId === id) setActiveConfigId('');
    } catch {
      message.error('删除配置失败');
    }
  };

  // ── 编辑弹窗 ─────────────────────────────────────────────────────────────

  const openCreate = () => {
    setEditingId(null);
    setHasToken(false);
    form.resetFields();
    setYamlText(
      YAML_HINT +
        'edge:\n' +
        '  protocol: auto            # 可选: auto | quic | http2\n' +
        'logging:\n' +
        '  logLevel: info            # 可选: debug | info | warn | error | fatal\n'
    );
    setModalOpen(true);
  };

  const openEdit = async (id: string) => {
    setEditingId(id);
    try {
      const resp = await client.get<ConfigEnvelope>(`/api/v1/configs/${id}`);
      const env = resp.data;
      form.setFieldsValue({
        id: env.id,
        name: env.cfdmgr?.name || env.name || env.id,
        token: '', // 永不回填明文 token（后端已脱敏）；留空 = 保持现有
        manualStart: env.cfdmgr?.manualStart ?? false,
      });
      setHasToken(!!env.has_token);
      // 编辑时同样带上字段/取值参考注释，方便对照与复制粘贴（保存时被 parseYaml 丢弃）。
      setYamlText(YAML_HINT + configToYaml(env.config || {}));
    } catch {
      message.error('获取配置详情失败');
      return;
    }
    setModalOpen(true);
  };

  const handleSave = async () => {
    let values: EditFormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }

    // 用真 YAML 解析（支持嵌套 edge/reliability/logging/identity）。
    let parsed: Record<string, unknown>;
    try {
      parsed = (parseYaml(yamlText) as Record<string, unknown>) || {};
      if (typeof parsed !== 'object' || Array.isArray(parsed)) {
        throw new Error('配置必须是 YAML 映射（key: value）');
      }
    } catch (e) {
      message.error('YAML 解析失败：' + (e as Error).message);
      return;
    }
    // token 永远由专用字段管理：先从 YAML 剔除，仅当用户填写了才提交；
    // 留空时后端会保留实例现有 token。用户可粘贴整条安装命令，提取出裸 token。
    delete parsed.token;
    if (values.token) parsed.token = extractCloudflaredToken(values.token);

    const cfdmgr: MgrMeta = {
      name: values.name || (editingId ?? values.id),
      manualStart: !!values.manualStart,
    };

    setSaving(true);
    try {
      if (editingId) {
        await client.put(`/api/v1/configs/${editingId}`, {
          config: parsed,
          cfdmgr,
        });
        message.success('配置保存成功！');
      } else {
        await client.post('/api/v1/configs', {
          id: values.id,
          config: parsed,
          cfdmgr,
        });
        message.success('配置创建成功！');
        setActiveConfigId(values.id);
      }
      setModalOpen(false);
      fetchConfigs();
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
      message.error('保存失败: ' + (e.response?.data?.error?.message || e.message || ''));
    } finally {
      setSaving(false);
    }
  };

  // ── 复制弹窗 ─────────────────────────────────────────────────────────────

  const openDuplicate = (id: string) => {
    setDupSourceId(id);
    setDupNewId(`${id}_copy`);
    setDupModalOpen(true);
  };

  const handleDuplicate = async () => {
    if (!dupNewId.trim()) { message.warning('请输入新配置 ID'); return; }
    try {
      await client.post(`/api/v1/configs/${dupSourceId}/duplicate`, { new_id: dupNewId });
      message.success(`已复制为: ${dupNewId}`);
      setDupModalOpen(false);
      fetchConfigs();
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
      message.error('复制失败: ' + (e.response?.data?.error?.message || e.message));
    }
  };

  // ── 状态徽章 ─────────────────────────────────────────────────────────────

  const getStatusBadge = (state?: string) => {
    switch (state) {
      case 'started':
        return <Badge status="success" text={<span style={{ color: '#52c41a' }}>运行中</span>} />;
      case 'starting':
        return <Badge status="processing" text={<span style={{ color: '#1677ff' }}>启动中</span>} />;
      case 'stopping':
        return <Badge status="processing" text={<span style={{ color: '#faad14' }}>停止中</span>} />;
      default:
        return <Badge status="default" text="已停止" />;
    }
  };

  const activeSnap = configs.find((c) => c.id === activeConfigId);

  // 实时识别 token 输入框里贴的内容（支持整条安装命令）。
  const watchedToken: string | undefined = Form.useWatch('token', form);
  const tokenHint = useMemo(() => {
    const raw = (watchedToken || '').trim();
    if (raw) {
      const t = extractCloudflaredToken(raw);
      const looksToken = /^eyJ[A-Za-z0-9_\-+/=]+$/.test(t) && t.length >= 100;
      if (looksToken) {
        const mask = `${t.slice(0, 4)}…${t.slice(-4)}`;
        return `✓ 已识别 token ${mask}（${t.length} 字符）${t !== raw ? ' · 已从命令中自动提取' : ''}`;
      }
      return '⚠ 未识别到 cloudflared token（应为 eyJ 开头、≥100 字符的 base64）';
    }
    return editingId
      ? hasToken
        ? '已设置；留空保持不变。可直接粘贴整条安装命令，自动提取 token'
        : '尚未设置。可粘贴裸 token 或整条安装命令，自动提取 token'
      : '可粘贴裸 token，或整条安装命令（service install / tunnel run --token / docker run …），自动提取';
  }, [watchedToken, editingId, hasToken]);

  return (
    <div style={{ height: '100%' }}>
      <Row gutter={16} style={{ height: '100%', minHeight: 580 }}>
        {/* 左栏：实例列表（可拖动排序，可一键缩窄） */}
        <Col xs={24} md={listCollapsed ? 5 : 8} style={{ display: 'flex', flexDirection: 'column', transition: 'all .2s' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16, gap: 8 }}>
            <Space size={6}>
              <Tooltip title={listCollapsed ? '展开列表' : '缩窄列表'}>
                <Button type="text" size="small" icon={listCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />} onClick={toggleListCollapsed} />
              </Tooltip>
              {!listCollapsed && <Title level={4} style={{ margin: 0 }}>隧道实例</Title>}
            </Space>
            {listCollapsed ? (
              <Tooltip title="新建实例"><Button type="primary" size="small" icon={<PlusOutlined />} onClick={openCreate} /></Tooltip>
            ) : (
              <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建</Button>
            )}
          </div>

          <div style={{ flex: 1, overflowY: 'auto', paddingRight: 4 }}>
            {configs.length === 0 ? (
              <Card style={{ textAlign: 'center', padding: '40px 0', borderRadius: 10 }}>
                <Empty description="暂无 cloudflared 隧道配置，点击右上角创建。" />
              </Card>
            ) : (
              <List
                dataSource={configs}
                renderItem={(item) => {
                  const isActive = item.id === activeConfigId;
                  const isRunning = item.state === 'started';
                  const isOver = overId === item.id && dragId !== item.id;
                  const procState = item.state === 'starting' || item.state === 'stopping';
                  return (
                    <div
                      // 整卡不再 draggable；发起拖动只在把手（下方 HolderOutlined），
                      // 避免点击启停/编辑/删除等正常内容时误触发排序。卡片仅作为拖放目标。
                      onDragOver={(e) => { if (dragId == null) return; e.preventDefault(); if (overId !== item.id) setOverId(item.id); }}
                      onDrop={(e) => { if (dragId == null) return; e.preventDefault(); handleReorderDrop(item.id); }}
                      style={{
                        borderTop: isOver ? `2px solid ${themeToken.colorPrimary}` : '2px solid transparent',
                        opacity: dragId === item.id ? 0.45 : 1,
                        transition: 'opacity .15s',
                      }}
                    >
                      <Card
                        hoverable
                        style={{
                          marginBottom: 12,
                          cursor: 'pointer',
                          border: `1px solid ${isActive ? themeToken.colorPrimary : themeToken.colorBorderSecondary}`,
                          background: isActive ? themeToken.colorPrimaryBg : themeToken.colorBgContainer,
                          borderRadius: 10,
                        }}
                        onClick={() => setActiveConfigId(item.id)}
                        styles={{ body: { padding: listCollapsed ? 10 : 16 } }}
                      >
                        {listCollapsed ? (
                          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                            <span
                              draggable
                              onClick={(e) => e.stopPropagation()}
                              onDragStart={(e) => { e.stopPropagation(); setDragId(item.id); e.dataTransfer.effectAllowed = 'move'; }}
                              onDragEnd={() => { setDragId(null); setOverId(null); }}
                              style={{ cursor: 'grab', display: 'inline-flex', flex: '0 0 auto' }}
                              title="拖动排序"
                            >
                              <HolderOutlined style={{ color: themeToken.colorTextQuaternary }} />
                            </span>
                            <Tooltip title={`${item.name || item.id}（${item.id}）`}>
                              <Text strong ellipsis style={{ flex: 1, minWidth: 0, fontSize: 13 }}>{item.name || item.id}</Text>
                            </Tooltip>
                            <Badge status={isRunning ? 'success' : procState ? 'processing' : 'default'} />
                          </div>
                        ) : (
                          <>
                            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'start', marginBottom: 8 }}>
                              <div style={{ display: 'flex', alignItems: 'start', gap: 8, minWidth: 0 }}>
                                <span
                                  draggable
                                  onClick={(e) => e.stopPropagation()}
                                  onDragStart={(e) => { e.stopPropagation(); setDragId(item.id); e.dataTransfer.effectAllowed = 'move'; }}
                                  onDragEnd={() => { setDragId(null); setOverId(null); }}
                                  style={{ cursor: 'grab', display: 'inline-flex', marginTop: 4, flex: '0 0 auto' }}
                                  title="拖动排序"
                                >
                                  <HolderOutlined style={{ color: themeToken.colorTextQuaternary }} />
                                </span>
                                <div style={{ minWidth: 0 }}>
                                  <Text strong style={{ fontSize: 15 }}>{item.name || item.id}</Text>
                                  <div><Text type="secondary" style={{ fontSize: 12 }}>ID: {item.id}</Text></div>
                                </div>
                              </div>
                              {getStatusBadge(item.state)}
                            </div>

                            <div style={{ borderBottom: `1px solid ${themeToken.colorBorderSecondary}`, margin: '8px 0' }} />

                            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                              <Space>
                                {isRunning ? (
                                  <Button type="primary" danger size="small" icon={<StopOutlined />}
                                    loading={statusLoading[item.id]}
                                    onClick={(e) => { e.stopPropagation(); handleStop(item.id); }}>
                                    停止
                                  </Button>
                                ) : (
                                  <Button type="primary" size="small" icon={<PlayCircleOutlined />}
                                    loading={statusLoading[item.id]}
                                    style={{ background: '#52c41a', borderColor: '#52c41a' }}
                                    onClick={(e) => { e.stopPropagation(); handleStart(item.id); }}>
                                    启动
                                  </Button>
                                )}
                                {isRunning && (
                                  <Tooltip title="重启">
                                    <Button size="small" icon={<ReloadOutlined />}
                                      loading={statusLoading[item.id]}
                                      onClick={(e) => { e.stopPropagation(); handleReload(item.id); }} />
                                  </Tooltip>
                                )}
                              </Space>

                              <Space>
                                <Tooltip title="编辑配置">
                                  <Button size="small" type="text" icon={<EditOutlined />}
                                    onClick={(e) => { e.stopPropagation(); openEdit(item.id); }} />
                                </Tooltip>
                                <Tooltip title="克隆配置">
                                  <Button size="small" type="text" icon={<CopyOutlined />}
                                    onClick={(e) => { e.stopPropagation(); openDuplicate(item.id); }} />
                                </Tooltip>
                                <Popconfirm
                                  title={`确定删除「${item.name || item.id}」？`}
                                  description="删除后不可恢复。"
                                  onConfirm={() => handleDelete(item.id)}
                                  onPopupClick={(e) => e.stopPropagation()}
                                  okText="删除" okType="danger" cancelText="取消"
                                >
                                  <Button size="small" type="text" danger icon={<DeleteOutlined />}
                                    onClick={(e) => e.stopPropagation()} />
                                </Popconfirm>
                              </Space>
                            </div>
                          </>
                        )}
                      </Card>
                    </div>
                  );
                }}
              />
            )}
          </div>
        </Col>

        {/* 右栏：实例详情 */}
        <Col xs={24} md={listCollapsed ? 19 : 16} style={{ transition: 'all .2s' }}>
          {activeSnap ? (
            <InstanceDetailPanel
              snap={activeSnap}
              loading={statusLoading[activeSnap.id]}
              onStart={() => handleStart(activeSnap.id)}
              onStop={() => handleStop(activeSnap.id)}
              onReload={() => handleReload(activeSnap.id)}
              onEdit={() => openEdit(activeSnap.id)}
              onDuplicate={() => openDuplicate(activeSnap.id)}
              onDelete={() => handleDelete(activeSnap.id)}
            />
          ) : (
            <Card style={{ height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '100px 0', borderRadius: 10 }}>
              <Empty description="请在左侧选择或创建一个 cloudflared 隧道配置。" />
            </Card>
          )}
        </Col>
      </Row>

      {/* 新建 / 编辑 Modal */}
      <Modal
        title={editingId ? `编辑配置 — ${editingId}` : '新建 cloudflared 隧道配置'}
        open={modalOpen}
        onOk={handleSave}
        confirmLoading={saving}
        onCancel={() => setModalOpen(false)}
        okText="保存" cancelText="取消"
        destroyOnClose
        width={720}
      >
        <Form form={form} layout="vertical" style={{ marginTop: 8 }}>
          {!editingId && (
            <Form.Item
              label="唯一 ID（纯英文/数字/下划线/中划线）"
              name="id"
              rules={[
                { required: true, message: '请输入配置 ID' },
                { pattern: /^[a-zA-Z0-9_-]+$/, message: '仅支持英文字母、数字、下划线及中划线' },
              ]}
            >
              <Input placeholder="例如: my-tunnel" />
            </Form.Item>
          )}
          <Form.Item label="显示名称" name="name">
            <Input placeholder="例如: 生产隧道" />
          </Form.Item>
          <Form.Item label="Cloudflared Token" name="token" extra={tokenHint}>
            <Input.TextArea
              autoSize={{ minRows: 2, maxRows: 5 }}
              placeholder={
                editingId && hasToken
                  ? '留空 = 保持现有 token；或粘贴新 token / 整条安装命令'
                  : '粘贴裸 token，或整条安装命令（如 cloudflared service install eyJ… / tunnel run --token eyJ…），保存时自动提取'
              }
              onBlur={() => {
                const v = form.getFieldValue('token');
                if (v) {
                  const t = extractCloudflaredToken(v);
                  if (t !== v) form.setFieldValue('token', t);
                }
              }}
            />
          </Form.Item>
          <Form.Item label="手动启动" name="manualStart" valuePropName="checked" initialValue={false}>
            <Switch checkedChildren="手动启动" unCheckedChildren="随服务启动" />
          </Form.Item>
          <Form.Item label="YAML 配置（完整 cloudflared 配置）">
            <div
              style={{
                border: `1px solid ${themeMode === 'dark' ? themeToken.colorBorderSecondary : '#1f2933'}`,
                borderRadius: 8,
                overflow: 'hidden',
                background: '#0b0f14',
              }}
            >
              <CodeMirror
                value={yamlText}
                onChange={setYamlText}
                theme={oneDark}
                extensions={yamlExtensions}
                height="300px"
                basicSetup={{
                  lineNumbers: true,
                  foldGutter: true,
                  highlightActiveLine: true,
                  bracketMatching: true,
                  autocompletion: false,
                  tabSize: 2,
                }}
              />
            </div>
          </Form.Item>
        </Form>
      </Modal>

      {/* 复制 Modal */}
      <Modal
        title={`克隆配置 — ${dupSourceId}`}
        open={dupModalOpen}
        onOk={handleDuplicate}
        onCancel={() => setDupModalOpen(false)}
        okText="确认克隆" cancelText="取消"
        destroyOnClose
      >
        <Form layout="vertical" style={{ marginTop: 8 }}>
          <Form.Item label="新配置的唯一 ID">
            <Input
              value={dupNewId}
              onChange={(e) => setDupNewId(e.target.value)}
              placeholder="例如: my-tunnel_copy"
            />
          </Form.Item>
        </Form>
      </Modal>

      {/* ExclamationCircleOutlined 仅 Modal.confirm 用到，保留 import 消除 lint 警告 */}
      <span style={{ display: 'none' }}><ExclamationCircleOutlined /></span>
    </div>
  );
};

export default Configs;
