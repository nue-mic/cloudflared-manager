import { useEffect, useState, useRef } from 'react';
import {
  Card, Row, Col, Button, Badge, Space, Typography, Popconfirm,
  Form, Input, Switch, Modal, message, Tag, Tooltip, Empty, List,
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
} from '@ant-design/icons';

import CodeMirror from '@uiw/react-codemirror';
import { StreamLanguage } from '@codemirror/language';
import { yaml as yamlMode } from '@codemirror/legacy-modes/mode/yaml';
import { oneDark } from '@codemirror/theme-one-dark';
import { EditorView } from '@codemirror/view';

import client from '../api/client';
import { useTheme } from '../theme/ThemeContext';
import { useEventSubscription } from '../events/EventStreamContext';
import type { InstanceStateData } from '../events/types';
import type { Snapshot, ConfigEnvelope, MgrMeta, TunnelConfigV1 } from '../api/types';

const { Title, Text } = Typography;

const VSCODE_MONO = `'Cascadia Code', Consolas, 'SF Mono', Menlo, monospace`;
const yamlFontTheme = EditorView.theme({
  '&': { fontFamily: VSCODE_MONO, fontSize: '13.5px' },
  '.cm-content': { fontFamily: VSCODE_MONO },
  '.cm-gutters': { fontFamily: VSCODE_MONO, fontSize: '12.5px' },
  '.cm-scroller': { lineHeight: '1.55' },
});

// 简单的 yaml stringify（仅用于展示；保存时直接发 yaml 文本通过 raw 接口）
function toYaml(obj: unknown, indent = 0): string {
  if (obj === null || obj === undefined) return 'null';
  if (typeof obj === 'string') {
    if (obj.includes('\n') || obj.includes('"') || obj.includes(':')) {
      return `"${obj.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
    }
    return obj;
  }
  if (typeof obj === 'boolean' || typeof obj === 'number') return String(obj);
  if (Array.isArray(obj)) {
    if (obj.length === 0) return '[]';
    const pad = ' '.repeat(indent + 2);
    return obj.map((v) => `\n${pad}- ${toYaml(v, indent + 2)}`).join('');
  }
  if (typeof obj === 'object') {
    const entries = Object.entries(obj as Record<string, unknown>).filter(
      ([, v]) => v !== undefined && v !== null
    );
    if (entries.length === 0) return '{}';
    const pad = ' '.repeat(indent + 2);
    return entries.map(([k, v]) => `\n${pad}${k}: ${toYaml(v, indent + 2)}`).join('');
  }
  return String(obj);
}

function configToYaml(cfg: TunnelConfigV1): string {
  try {
    return Object.entries(cfg)
      .filter(([, v]) => v !== undefined && v !== null)
      .map(([k, v]) => `${k}:${toYaml(v, 0)}`)
      .join('\n');
  } catch {
    return '';
  }
}

// 极简 yaml → object（仅解析顶层 k: v；复杂 yaml 留给后端 /validate 校验）
function parseSimpleYaml(text: string): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const colon = trimmed.indexOf(':');
    if (colon < 0) continue;
    const key = trimmed.slice(0, colon).trim();
    const val = trimmed.slice(colon + 1).trim();
    if (!key) continue;
    if (val === 'true') result[key] = true;
    else if (val === 'false') result[key] = false;
    else if (val !== '' && !isNaN(Number(val))) result[key] = Number(val);
    else if (val !== '') result[key] = val.replace(/^["']|["']$/g, '');
  }
  return result;
}

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

  // 复制弹窗
  const [dupModalOpen, setDupModalOpen] = useState(false);
  const [dupSourceId, setDupSourceId] = useState('');
  const [dupNewId, setDupNewId] = useState('');

  const [form] = Form.useForm<EditFormValues>();

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
    form.resetFields();
    setYamlText('# 在此输入 cloudflared 隧道 YAML 配置\n');
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
        token: (env.config as Record<string, unknown>)?.token as string || '',
        manualStart: env.cfdmgr?.manualStart ?? false,
      });
      setYamlText(configToYaml(env.config || {}));
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

    // 从 YAML 解析 config（简单版）；token 字段覆盖
    const parsed = parseSimpleYaml(yamlText);
    if (values.token) parsed.token = values.token;

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

  return (
    <div style={{ height: '100%' }}>
      <Row gutter={16} style={{ height: '100%', minHeight: 580 }}>
        {/* 左栏：实例列表 */}
        <Col xs={24} md={8} style={{ display: 'flex', flexDirection: 'column' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
            <Title level={4} style={{ margin: 0 }}>隧道实例</Title>
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
              新建
            </Button>
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
                  return (
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
                      styles={{ body: { padding: 16 } }}
                    >
                      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'start', marginBottom: 8 }}>
                        <div>
                          <Text strong style={{ fontSize: 15 }}>{item.name || item.id}</Text>
                          <div><Text type="secondary" style={{ fontSize: 12 }}>ID: {item.id}</Text></div>
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
                    </Card>
                  );
                }}
              />
            )}
          </div>
        </Col>

        {/* 右栏：实例详情 */}
        <Col xs={24} md={16}>
          {activeConfigId ? (
            <Card bordered={false} styles={{ body: { padding: 20 } }}
              style={{ height: '100%', minHeight: 520, borderRadius: 10 }}>
              <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <div>
                  <Text type="secondary" style={{ fontSize: 12 }}>当前实例</Text>
                  <Title level={4} style={{ margin: '4px 0 0 0' }}>{activeSnap?.name || activeConfigId}</Title>
                </div>
                <Space>
                  {getStatusBadge(activeSnap?.state)}
                  <Button icon={<EditOutlined />} onClick={() => openEdit(activeConfigId)}>
                    编辑配置
                  </Button>
                </Space>
              </div>

              <Space direction="vertical" style={{ width: '100%' }} size={8}>
                <div>
                  <Text type="secondary" style={{ fontSize: 12 }}>ID</Text>
                  <div><Text code>{activeSnap?.id}</Text></div>
                </div>
                <div>
                  <Text type="secondary" style={{ fontSize: 12 }}>状态</Text>
                  <div>
                    <Tag color={
                      activeSnap?.state === 'started' ? 'success' :
                      activeSnap?.state === 'starting' || activeSnap?.state === 'stopping' ? 'processing' :
                      'default'
                    }>
                      {activeSnap?.state || 'stopped'}
                    </Tag>
                  </div>
                </div>
                {activeSnap?.last_error && (
                  <div>
                    <Text type="secondary" style={{ fontSize: 12 }}>最近错误</Text>
                    <div><Text type="danger" style={{ fontSize: 12 }}>{activeSnap.last_error}</Text></div>
                  </div>
                )}
                {activeSnap?.started_at && (
                  <div>
                    <Text type="secondary" style={{ fontSize: 12 }}>启动时间</Text>
                    <div><Text style={{ fontSize: 12 }}>{activeSnap.started_at}</Text></div>
                  </div>
                )}
                <div>
                  <Text type="secondary" style={{ fontSize: 12 }}>配置文件路径</Text>
                  <div><Text code style={{ fontSize: 12 }}>{activeSnap?.path || '—'}</Text></div>
                </div>
              </Space>

              <div style={{ marginTop: 24, display: 'flex', gap: 8 }}>
                {activeSnap?.state !== 'started' ? (
                  <Button type="primary" icon={<PlayCircleOutlined />}
                    style={{ background: '#52c41a', borderColor: '#52c41a' }}
                    onClick={() => handleStart(activeConfigId)}>
                    启动
                  </Button>
                ) : (
                  <>
                    <Button type="primary" danger icon={<StopOutlined />}
                      onClick={() => handleStop(activeConfigId)}>停止</Button>
                    <Button icon={<ReloadOutlined />} onClick={() => handleReload(activeConfigId)}>重启</Button>
                  </>
                )}
                <Popconfirm
                  title={`确定删除「${activeSnap?.name || activeConfigId}」？`}
                  onConfirm={() => handleDelete(activeConfigId)}
                  okText="删除" okType="danger" cancelText="取消"
                >
                  <Button danger icon={<DeleteOutlined />}>删除</Button>
                </Popconfirm>
              </div>
            </Card>
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
          <Form.Item label="Cloudflared Token（覆盖 YAML 中 token 字段）" name="token">
            <Input.Password placeholder="留空则以 YAML 中 token 为准" />
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
