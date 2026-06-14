// 实例详情「Cloudflare」面板。
//
// 进入时同时拉 getBinding + tokenInfo：
//  - 未绑定：展示从 token 解码出的 account_tag / tunnel_id，提供「关联到账号」表单。
//  - 已绑定：展示账号 / 隧道信息 + 解绑，并内嵌该实例的「公共主机名」管理表格
//    （走实例级聚合 API listPublicHostnames/add/update/delete，自动处理 ingress+DNS）。

import { useCallback, useEffect, useState } from 'react';
import {
  Card,
  Button,
  Space,
  Typography,
  Tag,
  Select,
  Input,
  Alert,
  Skeleton,
  Empty,
  Descriptions,
  Table,
  Popconfirm,
  Tooltip,
  App,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  PlusOutlined,
  ReloadOutlined,
  EditOutlined,
  DeleteOutlined,
  LinkOutlined,
  DisconnectOutlined,
  HolderOutlined,
} from '@ant-design/icons';
import { cfApi } from '../../api/client';
import type { CFAccountView, CFBinding, CFTokenInfo, CFPublicHostname } from '../../api/types';
import PublicHostnameModal from '../cf/PublicHostnameModal';
import {
  formToPayload,
  publicHostnameToForm,
  originRequestTags,
  reorderHostnames,
  type PublicHostnameFormValues,
} from '../../pages/cfIngress';

const { Text, Paragraph } = Typography;

interface Props {
  id: string;
}

export default function InstanceCFPanel({ id }: Props) {
  const { message } = App.useApp();

  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [binding, setBinding] = useState<CFBinding | null>(null);
  const [tokenInfo, setTokenInfo] = useState<CFTokenInfo | null>(null);

  // 账号列表（用于关联选择）。
  const [accounts, setAccounts] = useState<CFAccountView[]>([]);

  // 关联表单
  const [selAccount, setSelAccount] = useState<string>('');
  const [selTunnel, setSelTunnel] = useState<string>('');
  const [binding_, setBinding_] = useState(false);

  // 公共主机名
  const [hostnames, setHostnames] = useState<CFPublicHostname[]>([]);
  const [hostnamesLoading, setHostnamesLoading] = useState(false);
  const [dnsError, setDnsError] = useState<string>('');
  const [phModalOpen, setPhModalOpen] = useState(false);
  const [phEditing, setPhEditing] = useState<CFPublicHostname | null>(null);
  // 公共主机名拖动排序
  const [dragIdx, setDragIdx] = useState<number | null>(null);
  const [overIdx, setOverIdx] = useState<number | null>(null);
  // 正在手动同步 DNS 的行 index（按钮 loading）。
  const [syncingIdx, setSyncingIdx] = useState<number | null>(null);

  const errMsg = (err: unknown): string => {
    const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
    return e.response?.data?.error?.message || e.message || '未知错误';
  };

  const usableAccounts = accounts.filter((a) => a.status === 'active' && a.account_id);

  const loadHostnames = useCallback(async () => {
    setHostnamesLoading(true);
    try {
      const resp = await cfApi.listPublicHostnames(id);
      setHostnames(resp.data?.items || []);
      setDnsError(resp.data?.dns_error || '');
    } catch (err: unknown) {
      message.error('获取公共主机名失败：' + errMsg(err));
    } finally {
      setHostnamesLoading(false);
    }
  }, [id, message]);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [bindingResp, tokenResp] = await Promise.all([cfApi.getBinding(id), cfApi.tokenInfo(id)]);
      const b = bindingResp.data;
      const ti = tokenResp.data;
      setBinding(b);
      setTokenInfo(ti);
      // 关联表单默认值：tunnel_id 用 token 解码出的值。
      setSelTunnel(ti.tunnel_id || '');
      if (b.bound) {
        loadHostnames();
      }
    } catch (err: unknown) {
      message.error('加载 Cloudflare 关联失败：' + errMsg(err));
    } finally {
      setLoading(false);
    }
  }, [id, message, loadHostnames]);

  // 仅刷新关联/隧道状态（后端 BindingGet 会实时 GetTunnel 取最新 status），
  // 不触发整面板骨架屏，体验更轻；同时刷新公共主机名表。
  const refreshBinding = useCallback(async () => {
    setRefreshing(true);
    try {
      const resp = await cfApi.getBinding(id);
      setBinding(resp.data);
      if (resp.data.bound) loadHostnames();
      message.success('已刷新隧道状态');
    } catch (err: unknown) {
      message.error('刷新失败：' + errMsg(err));
    } finally {
      setRefreshing(false);
    }
  }, [id, message, loadHostnames]);

  useEffect(() => {
    // 账号列表只需拉一次（关联表单用）。
    cfApi
      .listAccounts()
      .then((r) => setAccounts(r.data?.items || []))
      .catch(() => setAccounts([]));
    load();
  }, [load]);

  const handleBind = async () => {
    if (!selAccount) {
      message.warning('请选择要关联的账号');
      return;
    }
    setBinding_(true);
    try {
      const resp = await cfApi.setBinding(id, {
        account_id: selAccount,
        tunnel_id: selTunnel.trim() || undefined,
      });
      setBinding(resp.data);
      if (resp.data.bound && !resp.data.match) {
        message.warning('已关联，但 token 与所选账号/隧道不一致，请核对');
      } else {
        message.success('已关联并校验通过');
      }
      if (resp.data.bound) loadHostnames();
    } catch (err: unknown) {
      message.error('关联失败：' + errMsg(err));
    } finally {
      setBinding_(false);
    }
  };

  const handleUnbind = async () => {
    try {
      await cfApi.deleteBinding(id);
      message.success('已解绑');
      setHostnames([]);
      load();
    } catch (err: unknown) {
      message.error('解绑失败：' + errMsg(err));
    }
  };

  // 公共主机名（实例级聚合 API：自动处理 ingress+DNS）。
  const handlePhSubmit = async (values: PublicHostnameFormValues) => {
    const payload = formToPayload(values);
    try {
      const resp = phEditing
        ? await cfApi.updatePublicHostname(id, phEditing.index, payload)
        : await cfApi.addPublicHostname(id, payload);
      message.success(phEditing ? '公共主机名已更新' : '公共主机名已添加');
      // 同步代理 CNAME 的结果必须显式提示：此前被静默吞掉，用户只看到 DNS「无记录」却不知为何
      // （常见原因：该 zone 不支持 Cloudflare 代理，如 .arpa 反向解析 zone；或 token 无 DNS 权限）。
      if (payload.manage_dns) {
        if (resp.data?.dns_error) {
          message.warning('公共主机名已保存，但 DNS 代理 CNAME 同步失败：' + resp.data.dns_error, 8);
        } else if (resp.data?.dns?.in_sync) {
          message.success('DNS 代理 CNAME 已同步');
        }
      }
      setPhModalOpen(false);
      setPhEditing(null);
      loadHostnames();
    } catch (err: unknown) {
      message.error('保存失败：' + errMsg(err));
      throw err;
    }
  };

  // 手动重新同步某条公共主机名的代理 CNAME（复用 update 接口重跑 ensureTunnelCNAME），
  // 把失败原因明确暴露给用户——用于 DNS 显示「无记录 / 不同步」时一键修复或诊断。
  const handleSyncDns = async (r: CFPublicHostname) => {
    setSyncingIdx(r.index);
    try {
      const resp = await cfApi.updatePublicHostname(id, r.index, {
        hostname: r.hostname,
        service: r.service,
        path: r.path,
        origin_request: r.origin_request,
        manage_dns: true,
      });
      if (resp.data?.dns_error) {
        message.warning('DNS 同步失败：' + resp.data.dns_error, 8);
      } else {
        message.success('DNS 代理 CNAME 已同步');
      }
      loadHostnames();
    } catch (err: unknown) {
      message.error('DNS 同步失败：' + errMsg(err));
    } finally {
      setSyncingIdx(null);
    }
  };

  const handlePhDelete = async (ph: CFPublicHostname) => {
    try {
      await cfApi.deletePublicHostname(id, ph.index, true);
      message.success('已删除');
      loadHostnames();
    } catch (err: unknown) {
      message.error('删除失败：' + errMsg(err));
    }
  };

  // 公共主机名拖动排序：按各行的 ingress 下标取权威规则重排 → putTunnelConfig（兜底恒末尾）。
  const handleHostnameReorder = async (toPos: number) => {
    const from = dragIdx;
    setDragIdx(null);
    setOverIdx(null);
    if (from == null || from === toPos || !binding) return;
    const aid = binding.account_id || '';
    const tid = binding.tunnel_id || '';
    if (!aid || !tid) {
      message.warning('缺少账号/隧道信息，无法排序');
      return;
    }
    const newOrder = [...hostnames];
    const [moved] = newOrder.splice(from, 1);
    newOrder.splice(toPos, 0, moved);
    setHostnames(newOrder); // 乐观
    try {
      const cfgResp = await cfApi.getTunnelConfig(aid, tid);
      const cfg = cfgResp.data?.config ?? {};
      const ingress = cfg.ingress ?? [];
      const orderedRules = newOrder.map((ph) => ingress[ph.index]).filter(Boolean);
      const next = reorderHostnames(cfg, orderedRules);
      await cfApi.putTunnelConfig(aid, tid, next);
      message.success('已保存新顺序');
    } catch (err: unknown) {
      message.error('保存顺序失败：' + errMsg(err));
    } finally {
      loadHostnames(); // 以服务端为准回正 + 刷新 index
    }
  };

  if (loading) return <Skeleton active />;

  // ── 未绑定 ──────────────────────────────────────────────────────────────────
  if (!binding?.bound) {
    return (
      <Space direction="vertical" size={14} style={{ width: '100%' }}>
        <Card size="small" title="Cloudflare 关联" style={{ borderRadius: 10 }}>
          {tokenInfo?.has_token ? (
            <Descriptions size="small" column={1} bordered style={{ marginBottom: 16 }}>
              <Descriptions.Item label="Token account_tag">
                {tokenInfo.account_tag ? (
                  <Text copyable style={{ fontFamily: 'monospace', fontSize: 12 }}>{tokenInfo.account_tag}</Text>
                ) : (
                  <Text type="secondary">—</Text>
                )}
              </Descriptions.Item>
              <Descriptions.Item label="Token tunnel_id">
                {tokenInfo.tunnel_id ? (
                  <Text copyable style={{ fontFamily: 'monospace', fontSize: 12 }}>{tokenInfo.tunnel_id}</Text>
                ) : (
                  <Text type="secondary">—</Text>
                )}
              </Descriptions.Item>
            </Descriptions>
          ) : (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 16 }}
              message="该实例尚未设置 token"
              description={tokenInfo?.error || '请先在实例配置中填写 cloudflared token，再关联到 Cloudflare 账号。'}
            />
          )}

          <Space direction="vertical" size={12} style={{ width: '100%' }}>
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>关联到账号</Text>
              <Select
                style={{ width: '100%', marginTop: 4 }}
                placeholder="选择一个已校验的 Cloudflare 账号"
                value={selAccount || undefined}
                onChange={setSelAccount}
                showSearch
                optionFilterProp="label"
                options={usableAccounts.map((a) => ({
                  value: a.id,
                  label: `${a.name} · ${a.account_name || a.account_id}`,
                }))}
                notFoundContent={<Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="无可用账号，请先在账号页校验" />}
              />
            </div>
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>Tunnel ID（可选，默认用 token 解码值）</Text>
              <Input
                style={{ marginTop: 4 }}
                placeholder="留空使用 token 中的 tunnel_id"
                value={selTunnel}
                onChange={(e) => setSelTunnel(e.target.value)}
              />
            </div>
            <Button type="primary" icon={<LinkOutlined />} loading={binding_} onClick={handleBind}>
              关联并校验
            </Button>
          </Space>
        </Card>
      </Space>
    );
  }

  // ── 已绑定 ──────────────────────────────────────────────────────────────────
  const phColumns: ColumnsType<CFPublicHostname> = [
    {
      title: '',
      key: 'drag',
      width: 32,
      // 只有把手可发起拖动；整行不再 draggable，避免点击/选中正常内容时误触发排序。
      render: (_v, _r, index) => (
        <span
          draggable
          onDragStart={(e) => { e.stopPropagation(); setDragIdx(index); }}
          onDragEnd={() => { setDragIdx(null); setOverIdx(null); }}
          style={{ cursor: 'grab', color: '#bbb', display: 'inline-flex', padding: '4px 2px' }}
          title="拖动排序"
        >
          <HolderOutlined />
        </span>
      ),
    },
    {
      title: '公共主机名',
      dataIndex: 'hostname',
      key: 'hostname',
      render: (v: string) => <Text strong>{v}</Text>,
    },
    {
      title: '路径',
      dataIndex: 'path',
      key: 'path',
      width: 110,
      render: (v: string) => (v ? <Text code>{v}</Text> : <Text type="secondary">/*</Text>),
    },
    {
      title: '服务',
      dataIndex: 'service',
      key: 'service',
      render: (v: string) => <Text code style={{ fontSize: 12 }}>{v}</Text>,
    },
    {
      title: 'DNS',
      key: 'dns',
      width: 150,
      render: (_, r) => {
        // 兜底规则（无 hostname）不涉及 DNS。
        if (!r.hostname) return <Text type="secondary">—</Text>;
        const needSync = !r.dns || !r.dns.exists || !r.dns.in_sync;
        return (
          <Space size={4}>
            {!r.dns ? (
              <Text type="secondary">—</Text>
            ) : !r.dns.exists ? (
              <Tag>无记录</Tag>
            ) : !r.dns.in_sync ? (
              <Tag color="warning">不同步</Tag>
            ) : (
              <Tag color="success">{r.dns.proxied ? '已代理' : '仅 DNS'}</Tag>
            )}
            {needSync && (
              <Tooltip title="重新同步指向本隧道的代理 CNAME（失败会提示具体原因）">
                <Button
                  type="link"
                  size="small"
                  style={{ padding: 0, height: 'auto' }}
                  loading={syncingIdx === r.index}
                  onClick={() => handleSyncDns(r)}
                >
                  同步
                </Button>
              </Tooltip>
            )}
          </Space>
        );
      },
    },
    {
      title: 'originRequest',
      key: 'origin',
      render: (_, r) => {
        const tags = originRequestTags(r.origin_request);
        return tags.length ? (
          <Space size={4} wrap>
            {tags.map((t) => (
              <Tag key={t} color="blue" style={{ fontSize: 11 }}>{t}</Tag>
            ))}
          </Space>
        ) : (
          <Text type="secondary">默认</Text>
        );
      },
    },
    {
      title: '操作',
      key: 'actions',
      width: 100,
      align: 'right',
      render: (_, r) => (
        <Space size={4}>
          <Tooltip title="编辑">
            <Button
              type="text"
              size="small"
              icon={<EditOutlined />}
              onClick={() => {
                setPhEditing(r);
                setPhModalOpen(true);
              }}
            />
          </Tooltip>
          <Popconfirm
            title="删除该公共主机名？"
            description="将移除 ingress 规则并删除对应 DNS 记录。"
            okText="删除"
            okButtonProps={{ danger: true }}
            cancelText="取消"
            onConfirm={() => handlePhDelete(r)}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Space direction="vertical" size={14} style={{ width: '100%' }}>
      <Card
        size="small"
        title="Cloudflare 关联"
        style={{ borderRadius: 10 }}
        extra={
          <Space>
            <Button size="small" icon={<ReloadOutlined />} loading={refreshing} onClick={refreshBinding}>
              刷新
            </Button>
            <Popconfirm
              title="解除关联？"
              description="仅断开本实例与该账号/隧道的关联，不影响 Cloudflare 上的隧道。"
              okText="解绑"
              okButtonProps={{ danger: true }}
              cancelText="取消"
              onConfirm={handleUnbind}
            >
              <Button size="small" danger icon={<DisconnectOutlined />}>解绑</Button>
            </Popconfirm>
          </Space>
        }
      >
        {!binding.match && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 12 }}
            message="token 与所关联的账号 / 隧道不一致"
            description="本实例运行用的 token 解码出的 account/tunnel 与所选关联不匹配，公共主机名操作可能落到非预期隧道，请核对。"
          />
        )}
        <Descriptions size="small" column={1} bordered>
          <Descriptions.Item label="账号">
            {binding.account_name || binding.account_id || <Text type="secondary">—</Text>}
          </Descriptions.Item>
          <Descriptions.Item label="隧道">
            <Space>
              <Text>{binding.tunnel_name || <Text type="secondary">—</Text>}</Text>
              {binding.tunnel?.status && (
                <Tag color={binding.tunnel.status === 'healthy' ? 'success' : binding.tunnel.status === 'down' ? 'error' : 'warning'}>
                  {binding.tunnel.status}
                </Tag>
              )}
            </Space>
          </Descriptions.Item>
          <Descriptions.Item label="Tunnel ID">
            {binding.tunnel_id ? (
              <Paragraph copyable style={{ margin: 0, fontFamily: 'monospace', fontSize: 12 }}>{binding.tunnel_id}</Paragraph>
            ) : (
              <Text type="secondary">—</Text>
            )}
          </Descriptions.Item>
        </Descriptions>
      </Card>

      <Card
        size="small"
        title="公共主机名"
        style={{ borderRadius: 10 }}
        extra={
          <Space>
            <Button size="small" icon={<ReloadOutlined />} onClick={loadHostnames} loading={hostnamesLoading}>
              刷新
            </Button>
            <Button
              size="small"
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => {
                setPhEditing(null);
                setPhModalOpen(true);
              }}
            >
              添加
            </Button>
          </Space>
        }
      >
        {dnsError && (
          <Alert type="warning" showIcon style={{ marginBottom: 12 }} message="DNS 状态获取部分失败" description={dnsError} />
        )}
        <Table<CFPublicHostname>
          size="small"
          rowKey="index"
          columns={phColumns}
          dataSource={hostnames}
          loading={hostnamesLoading}
          pagination={false}
          onRow={(_, index) => ({
            // 行仅作为拖放目标（drop target）；发起拖动只在把手列。
            onDragOver: (e) => { if (dragIdx == null) return; e.preventDefault(); if (index != null && overIdx !== index) setOverIdx(index); },
            onDrop: (e) => { if (dragIdx == null) return; e.preventDefault(); if (index != null) handleHostnameReorder(index); },
            style: {
              background: overIdx === index && dragIdx !== index ? 'rgba(24,144,255,0.10)' : undefined,
            },
          })}
          locale={{
            emptyText: (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无公共主机名，点击「添加」配置回源。" />
            ),
          }}
        />
      </Card>

      <PublicHostnameModal
        open={phModalOpen}
        aid={binding.account_id || ''}
        title={phEditing ? '编辑公共主机名' : '添加公共主机名'}
        initial={phEditing ? publicHostnameToForm(phEditing) : undefined}
        onCancel={() => {
          setPhModalOpen(false);
          setPhEditing(null);
        }}
        onSubmit={handlePhSubmit}
      />
    </Space>
  );
}
