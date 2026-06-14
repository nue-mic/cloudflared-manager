// 隧道「公共主机名」Tab（CFConsole 用）。
//
// CFConsole 按「账号 → 隧道」浏览，未必有本地实例，所以这里直接操作远端隧道
// 配置：getTunnelConfig 读 ingress → 改 → putTunnelConfig 整体替换；DNS 同步
// 在这一侧另用 listZones + createDNS 自行完成（manage_dns 开关控制）。

import { useCallback, useEffect, useState } from 'react';
import { Card, Table, Button, Space, Tag, Empty, Typography, App, Popconfirm, Tooltip } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { PlusOutlined, ReloadOutlined, EditOutlined, DeleteOutlined, HolderOutlined } from '@ant-design/icons';
import { cfApi } from '../../api/client';
import type { CFTunnelConfig, CFIngressRule } from '../../api/types';
import {
  listHostnameRules,
  appendHostnameRule,
  replaceRuleAt,
  removeRuleAt,
  reorderHostnames,
  formToIngressRule,
  ingressRuleToForm,
  originRequestTags,
  syncProxyCNAME,
  deleteProxyCNAME,
  type PublicHostnameFormValues,
} from '../../pages/cfIngress';
import PublicHostnameModal from './PublicHostnameModal';

const { Text } = Typography;

interface Props {
  aid: string;
  tid: string;
}

interface RowItem {
  rule: CFIngressRule;
  index: number;
}

export default function PublicHostnamesTab({ aid, tid }: Props) {
  const { message } = App.useApp();
  const [config, setConfig] = useState<CFTunnelConfig | null>(null);
  const [loading, setLoading] = useState(false);

  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<RowItem | null>(null);

  // 拖动排序（行内）
  const [dragIdx, setDragIdx] = useState<number | null>(null);
  const [overIdx, setOverIdx] = useState<number | null>(null);

  const errMsg = (err: unknown): string => {
    const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
    return e.response?.data?.error?.message || e.message || '未知错误';
  };

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await cfApi.getTunnelConfig(aid, tid);
      setConfig(resp.data?.config ?? {});
    } catch (err: unknown) {
      message.error('加载公共主机名失败：' + errMsg(err));
    } finally {
      setLoading(false);
    }
  }, [aid, tid, message]);

  useEffect(() => {
    load();
  }, [load]);

  const rows: RowItem[] = listHostnameRules(config).map(({ rule, index }) => ({ rule, index }));

  // 保存（新增 / 编辑）：改 ingress → putTunnelConfig → 可选同步 DNS。
  const handleSubmit = async (values: PublicHostnameFormValues) => {
    const rule = formToIngressRule(values);
    const nextConfig = editing
      ? replaceRuleAt(config, editing.index, rule)
      : appendHostnameRule(config, rule);
    try {
      const resp = await cfApi.putTunnelConfig(aid, tid, nextConfig);
      setConfig(resp.data?.config ?? nextConfig);
      message.success(editing ? '公共主机名已更新' : '公共主机名已添加');
      setModalOpen(false);
      setEditing(null);
    } catch (err: unknown) {
      message.error('保存失败：' + errMsg(err));
      throw err; // 让 Modal 保持打开
    }
    // DNS 同步：非致命，失败仅 warning。
    if (values.manage_dns) {
      try {
        await syncProxyCNAME(aid, tid, rule.hostname || '');
        message.success('已同步代理 CNAME');
      } catch (err: unknown) {
        message.warning('隧道配置已保存，但 DNS 同步失败：' + errMsg(err));
      }
    }
  };

  const handleDelete = async (item: RowItem) => {
    const nextConfig = removeRuleAt(config, item.index);
    try {
      const resp = await cfApi.putTunnelConfig(aid, tid, nextConfig);
      setConfig(resp.data?.config ?? nextConfig);
      message.success('已删除');
    } catch (err: unknown) {
      message.error('删除失败：' + errMsg(err));
      return;
    }
    // 尝试删除对应 DNS（非致命）。
    try {
      await deleteProxyCNAME(aid, item.rule.hostname || '');
    } catch {
      /* 静默：DNS 删除失败不阻断 */
    }
  };

  // 行拖动排序：重排 ingress（兜底规则恒末尾）→ putTunnelConfig，乐观更新失败回滚。
  const handleRowDrop = async (toPos: number) => {
    const from = dragIdx;
    setDragIdx(null);
    setOverIdx(null);
    if (from == null || from === toPos) return;
    const newRows = [...rows];
    const [moved] = newRows.splice(from, 1);
    newRows.splice(toPos, 0, moved);
    const nextConfig = reorderHostnames(config, newRows.map((r) => r.rule));
    const prev = config;
    setConfig(nextConfig); // 乐观
    try {
      const resp = await cfApi.putTunnelConfig(aid, tid, nextConfig);
      setConfig(resp.data?.config ?? nextConfig);
      message.success('已保存新顺序');
    } catch (err: unknown) {
      setConfig(prev);
      message.error('保存顺序失败：' + errMsg(err));
    }
  };

  const columns: ColumnsType<RowItem> = [
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
      key: 'hostname',
      render: (_, r) => <Text strong>{r.rule.hostname}</Text>,
    },
    {
      title: '路径',
      key: 'path',
      width: 120,
      render: (_, r) => (r.rule.path ? <Text code>{r.rule.path}</Text> : <Text type="secondary">/*</Text>),
    },
    {
      title: '服务',
      key: 'service',
      render: (_, r) => <Text code style={{ fontSize: 12 }}>{r.rule.service}</Text>,
    },
    {
      title: 'originRequest',
      key: 'origin',
      render: (_, r) => {
        const tags = originRequestTags(r.rule.originRequest);
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
                setEditing(r);
                setModalOpen(true);
              }}
            />
          </Tooltip>
          <Popconfirm
            title="删除该公共主机名？"
            description="将从隧道 ingress 移除该规则，并尝试删除对应代理 CNAME。"
            okText="删除"
            okButtonProps={{ danger: true }}
            cancelText="取消"
            onConfirm={() => handleDelete(r)}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card
      size="small"
      style={{ borderRadius: 10 }}
      title="公共主机名（Public Hostnames）"
      extra={
        <Space>
          <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={loading}>
            刷新
          </Button>
          <Button
            size="small"
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => {
              setEditing(null);
              setModalOpen(true);
            }}
          >
            添加公共主机名
          </Button>
        </Space>
      }
    >
      <Table<RowItem>
        size="small"
        rowKey={(r) => String(r.index)}
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={false}
        onRow={(_, index) => ({
          // 行仅作为拖放目标（drop target）；发起拖动只在把手列。
          onDragOver: (e) => { if (dragIdx == null) return; e.preventDefault(); if (index != null && overIdx !== index) setOverIdx(index); },
          onDrop: (e) => { if (dragIdx == null) return; e.preventDefault(); if (index != null) handleRowDrop(index); },
          style: {
            background: overIdx === index && dragIdx !== index ? 'rgba(24,144,255,0.10)' : undefined,
          },
        })}
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description="暂无公共主机名，点击「添加公共主机名」配置一条 ingress 规则。"
            />
          ),
        }}
      />

      <PublicHostnameModal
        open={modalOpen}
        aid={aid}
        title={editing ? '编辑公共主机名' : '添加公共主机名'}
        initial={editing ? ingressRuleToForm(editing.rule) : undefined}
        onCancel={() => {
          setModalOpen(false);
          setEditing(null);
        }}
        onSubmit={handleSubmit}
      />
    </Card>
  );
}
