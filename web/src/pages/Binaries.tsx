import { useCallback, useEffect, useState } from 'react';
import {
  Card,
  Table,
  Button,
  Space,
  Typography,
  Tag,
  Modal,
  Select,
  Popconfirm,
  Empty,
  Alert,
  Tooltip,
  App,
  theme as antdTheme,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ReloadOutlined,
  CloudDownloadOutlined,
  CheckCircleOutlined,
  DeleteOutlined,
} from '@ant-design/icons';

import { binariesApi } from '../api/client';
import type { BinaryItem, AvailableRelease } from '../api/types';
import { fmtDateTime } from '../utils/time';

const { Title, Text } = Typography;

function formatBytes(n?: number): string {
  if (!n || n <= 0) return '—';
  const units = ['B', 'KB', 'MB', 'GB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i === 0 ? 0 : 2)} ${units[i]}`;
}

const Binaries: React.FC = () => {
  const { token } = antdTheme.useToken();
  const { message } = App.useApp();

  const [items, setItems] = useState<BinaryItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // 检查可下载
  const [checkModalOpen, setCheckModalOpen] = useState(false);
  const [available, setAvailable] = useState<AvailableRelease[]>([]);
  const [checkLoading, setCheckLoading] = useState(false);
  const [selectedVer, setSelectedVer] = useState<string>('');
  const [installing, setInstalling] = useState(false);

  // 操作 loading
  const [actionLoading, setActionLoading] = useState<Record<string, boolean>>({});

  const loadList = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await binariesApi.list();
      setItems(resp.data?.items || []);
    } catch (err: unknown) {
      const e = err as { response?: { status?: number; data?: { error?: { message?: string } } } };
      if (e.response?.status === 404 || e.response?.status === 501) {
        setError('当前版本的后端不支持二进制管理 API，请升级 cfdmgrd。');
      } else {
        setError('获取二进制列表失败：' + (e.response?.data?.error?.message || '未知错误'));
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { loadList(); }, [loadList]);

  const handleCheckAvailable = async () => {
    setCheckLoading(true);
    try {
      const resp = await binariesApi.available();
      const releases = resp.data?.available || [];
      setAvailable(releases);
      setSelectedVer(releases[0]?.version || '');
      setCheckModalOpen(true);
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
      message.error('获取可用版本失败: ' + (e.response?.data?.error?.message || (e as Error).message));
    } finally {
      setCheckLoading(false);
    }
  };

  const handleInstall = async () => {
    if (!selectedVer) { message.warning('请选择要安装的版本'); return; }
    setInstalling(true);
    try {
      await binariesApi.install(selectedVer);
      message.success(`cloudflared ${selectedVer} 安装成功`);
      setCheckModalOpen(false);
      loadList();
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
      message.error('安装失败: ' + (e.response?.data?.error?.message || (e as Error).message));
    } finally {
      setInstalling(false);
    }
  };

  const handleActivate = async (version: string) => {
    setActionLoading((p) => ({ ...p, [version]: true }));
    try {
      await binariesApi.activate(version);
      message.success(`已将 ${version} 设为当前版本`);
      loadList();
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
      message.error('设置失败: ' + (e.response?.data?.error?.message || (e as Error).message));
    } finally {
      setActionLoading((p) => ({ ...p, [version]: false }));
    }
  };

  const handleDelete = async (version: string) => {
    setActionLoading((p) => ({ ...p, [version]: true }));
    try {
      await binariesApi.delete(version);
      message.success(`已删除 ${version}`);
      loadList();
    } catch (err: unknown) {
      const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
      message.error('删除失败: ' + (e.response?.data?.error?.message || (e as Error).message));
    } finally {
      setActionLoading((p) => ({ ...p, [version]: false }));
    }
  };

  const columns: ColumnsType<BinaryItem> = [
    {
      title: '版本',
      dataIndex: 'version',
      key: 'version',
      render: (v: string, r) => (
        <Space size={6}>
          <Text strong style={{ fontFamily: 'monospace' }}>{v}</Text>
          {r.is_active && <Tag color="success" icon={<CheckCircleOutlined />}>当前</Tag>}
          {r.verified && <Tag color="processing">已验证</Tag>}
        </Space>
      ),
    },
    {
      title: '大小',
      dataIndex: 'size',
      key: 'size',
      width: 100,
      render: (v: number) => formatBytes(v),
    },
    {
      title: '下载时间',
      dataIndex: 'downloaded_at',
      key: 'downloaded_at',
      width: 180,
      render: (v: string) => fmtDateTime(v),
    },
    {
      title: '路径',
      dataIndex: 'path',
      key: 'path',
      ellipsis: true,
      render: (v: string) => (
        <Tooltip title={v}>
          <Text code style={{ fontSize: 12 }}>{v}</Text>
        </Tooltip>
      ),
    },
    {
      title: '操作',
      key: 'actions',
      width: 160,
      align: 'right',
      render: (_, r) => (
        <Space size={4}>
          {!r.is_active && (
            <Tooltip title="设为当前版本">
              <Button
                type="primary"
                size="small"
                loading={actionLoading[r.version]}
                onClick={() => handleActivate(r.version)}
              >
                设为当前
              </Button>
            </Tooltip>
          )}
          {!r.is_active && (
            <Popconfirm
              title={`删除 ${r.version}？`}
              okText="删除" okButtonProps={{ danger: true }} cancelText="取消"
              onConfirm={() => handleDelete(r.version)}
            >
              <Button
                type="text"
                size="small"
                danger
                icon={<DeleteOutlined />}
                loading={actionLoading[r.version]}
              />
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ];

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
        <Title level={4} style={{ margin: 0 }}>二进制管理</Title>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={loadList} loading={loading}>
            刷新
          </Button>
          <Button
            type="primary"
            icon={<CloudDownloadOutlined />}
            loading={checkLoading}
            onClick={handleCheckAvailable}
          >
            检查可下载版本
          </Button>
        </Space>
      </div>

      {error ? (
        <Alert
          type="warning"
          showIcon
          message="二进制管理 API 不可用"
          description={error}
        />
      ) : (
        <Card
          title={
            <Space>
              <CloudDownloadOutlined />
              <span>已安装的 cloudflared 版本</span>
            </Space>
          }
          bordered={false}
          style={{ borderRadius: 10 }}
          styles={{ header: { background: token.colorFillTertiary } }}
        >
          <Table<BinaryItem>
            size="small"
            rowKey="version"
            columns={columns}
            dataSource={items}
            loading={loading}
            pagination={false}
            locale={{
              emptyText: (
                <Empty
                  image={Empty.PRESENTED_IMAGE_SIMPLE}
                  description="暂无已安装的 cloudflared 二进制，点击「检查可下载版本」安装。"
                />
              ),
            }}
          />
        </Card>
      )}

      {/* 选择版本安装弹窗 */}
      <Modal
        title="选择要安装的 cloudflared 版本"
        open={checkModalOpen}
        onOk={handleInstall}
        confirmLoading={installing}
        onCancel={() => setCheckModalOpen(false)}
        okText="开始安装"
        cancelText="取消"
        destroyOnClose
      >
        {available.length === 0 ? (
          <Empty description="未找到可用版本" />
        ) : (
          <Space direction="vertical" style={{ width: '100%', marginTop: 8 }} size={12}>
            <Text type="secondary">共找到 {available.length} 个可用版本：</Text>
            <Select
              style={{ width: '100%' }}
              value={selectedVer}
              onChange={setSelectedVer}
              options={available.map((r) => ({
                value: r.version,
                label: (
                  <Space size={8}>
                    <Text strong style={{ fontFamily: 'monospace' }}>{r.version}</Text>
                    {r.pre_release && <Tag color="orange">预发布</Tag>}
                    {r.size ? <Text type="secondary" style={{ fontSize: 12 }}>{formatBytes(r.size)}</Text> : null}
                  </Space>
                ),
              }))}
            />
          </Space>
        )}
      </Modal>
    </Space>
  );
};

export default Binaries;
