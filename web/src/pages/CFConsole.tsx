import { useCallback, useEffect, useState } from 'react';
import {
  Row,
  Col,
  Card,
  Button,
  Space,
  Typography,
  Tag,
  Select,
  Empty,
  Modal,
  Input,
  Tabs,
  Tooltip,
  Popconfirm,
  App,
  theme as antdTheme,
} from 'antd';
import {
  PlusOutlined,
  ReloadOutlined,
  EditOutlined,
  DeleteOutlined,
  CloudOutlined,
  GlobalOutlined,
  ApiOutlined,
  SettingOutlined,
  KeyOutlined,
} from '@ant-design/icons';
import { Link } from 'react-router-dom';

import { cfApi } from '../api/client';
import type { CFAccountView, CFTunnel, CFTunnelStatus } from '../api/types';
import PublicHostnamesTab from '../components/cf/PublicHostnamesTab';
import TunnelConfigTab from '../components/cf/TunnelConfigTab';
import ConnectorsTab from '../components/cf/ConnectorsTab';
import TunnelTokenTab from '../components/cf/TunnelTokenTab';
import DNSRecordsTab from '../components/cf/DNSRecordsTab';

const { Title, Text } = Typography;

function tunnelStatusTag(status?: CFTunnelStatus) {
  switch (status) {
    case 'healthy':
      return <Tag color="success">健康</Tag>;
    case 'degraded':
      return <Tag color="warning">降级</Tag>;
    case 'down':
      return <Tag color="error">已断开</Tag>;
    default:
      return <Tag>未连接</Tag>;
  }
}

const CFConsole: React.FC = () => {
  const { token } = antdTheme.useToken();
  const { message } = App.useApp();

  const [accounts, setAccounts] = useState<CFAccountView[]>([]);
  const [accountsLoading, setAccountsLoading] = useState(false);
  const [aid, setAid] = useState<string>('');

  const [tunnels, setTunnels] = useState<CFTunnel[]>([]);
  const [tunnelsLoading, setTunnelsLoading] = useState(false);
  const [tid, setTid] = useState<string>('');

  // 新建隧道
  const [createOpen, setCreateOpen] = useState(false);
  const [newName, setNewName] = useState('');
  const [creating, setCreating] = useState(false);

  // 重命名
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameTarget, setRenameTarget] = useState<CFTunnel | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [renaming, setRenaming] = useState(false);

  const errMsg = (err: unknown): string => {
    const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
    return e.response?.data?.error?.message || e.message || '未知错误';
  };

  // 仅可用账号（已校验 + account_id 非空）。
  const usableAccounts = accounts.filter((a) => a.status === 'active' && a.account_id);

  const loadAccounts = useCallback(async () => {
    setAccountsLoading(true);
    try {
      const resp = await cfApi.listAccounts();
      const items = resp.data?.items || [];
      setAccounts(items);
      const usable = items.filter((a) => a.status === 'active' && a.account_id);
      setAid((prev) => (prev && usable.some((a) => a.id === prev) ? prev : usable[0]?.id || ''));
    } catch (err: unknown) {
      message.error('获取账号列表失败：' + errMsg(err));
    } finally {
      setAccountsLoading(false);
    }
  }, [message]);

  const loadTunnels = useCallback(
    async (accountId: string) => {
      if (!accountId) {
        setTunnels([]);
        setTid('');
        return;
      }
      setTunnelsLoading(true);
      try {
        const resp = await cfApi.listTunnels(accountId);
        const items = resp.data?.items || [];
        setTunnels(items);
        setTid((prev) => (prev && items.some((t) => t.id === prev) ? prev : items[0]?.id || ''));
      } catch (err: unknown) {
        message.error('获取隧道列表失败：' + errMsg(err));
        setTunnels([]);
      } finally {
        setTunnelsLoading(false);
      }
    },
    [message]
  );

  useEffect(() => {
    loadAccounts();
  }, [loadAccounts]);

  useEffect(() => {
    loadTunnels(aid);
  }, [aid, loadTunnels]);

  const handleCreate = async () => {
    if (!newName.trim()) {
      message.warning('请输入隧道名称');
      return;
    }
    setCreating(true);
    try {
      const resp = await cfApi.createTunnel(aid, newName.trim());
      message.success('隧道已创建');
      setCreateOpen(false);
      setNewName('');
      await loadTunnels(aid);
      if (resp.data?.id) setTid(resp.data.id);
    } catch (err: unknown) {
      message.error('创建隧道失败：' + errMsg(err));
    } finally {
      setCreating(false);
    }
  };

  const handleRename = async () => {
    if (!renameTarget || !renameValue.trim()) {
      message.warning('请输入新名称');
      return;
    }
    setRenaming(true);
    try {
      await cfApi.renameTunnel(aid, renameTarget.id, renameValue.trim());
      message.success('已重命名');
      setRenameOpen(false);
      setRenameTarget(null);
      loadTunnels(aid);
    } catch (err: unknown) {
      message.error('重命名失败：' + errMsg(err));
    } finally {
      setRenaming(false);
    }
  };

  const handleDeleteTunnel = async (t: CFTunnel) => {
    try {
      await cfApi.deleteTunnel(aid, t.id);
      message.success('隧道已删除');
      if (tid === t.id) setTid('');
      loadTunnels(aid);
    } catch (err: unknown) {
      message.error('删除隧道失败：' + errMsg(err));
    }
  };

  const activeTunnel = tunnels.find((t) => t.id === tid);

  const tabItems = activeTunnel
    ? [
        {
          key: 'hostnames',
          label: (<span><GlobalOutlined /> 公共主机名</span>),
          children: <PublicHostnamesTab key={`ph-${aid}-${tid}`} aid={aid} tid={tid} />,
        },
        {
          key: 'config',
          label: (<span><SettingOutlined /> 隧道配置</span>),
          children: <TunnelConfigTab key={`cfg-${aid}-${tid}`} aid={aid} tid={tid} />,
        },
        {
          key: 'connectors',
          label: (<span><ApiOutlined /> 连接器</span>),
          children: <ConnectorsTab key={`conn-${aid}-${tid}`} aid={aid} tid={tid} />,
        },
        {
          key: 'token',
          label: (<span><KeyOutlined /> 隧道 Token</span>),
          children: <TunnelTokenTab key={`tok-${aid}-${tid}`} aid={aid} tid={tid} />,
        },
        {
          key: 'dns',
          label: (<span><CloudOutlined /> DNS 记录</span>),
          children: <DNSRecordsTab key={`dns-${aid}`} aid={aid} />,
        },
      ]
    : [];

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
        <Space size={12} align="center">
          <Title level={4} style={{ margin: 0 }}>Cloudflare 后台</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            <ApiOutlined style={{ color: token.colorPrimary }} /> 直连管理隧道 / 公共主机名 / DNS
          </Text>
        </Space>
        <Space>
          <Select
            style={{ width: 260 }}
            placeholder="选择 Cloudflare 账号"
            value={aid || undefined}
            onChange={setAid}
            loading={accountsLoading}
            showSearch
            optionFilterProp="label"
            options={usableAccounts.map((a) => ({
              value: a.id,
              label: `${a.name} · ${a.account_name || a.account_id}`,
            }))}
          />
          <Button icon={<ReloadOutlined />} onClick={loadAccounts} loading={accountsLoading} />
        </Space>
      </div>

      {usableAccounts.length === 0 ? (
        <Card style={{ borderRadius: 10 }}>
          <Empty
            description={
              <Space direction="vertical">
                <Text>暂无可用的 Cloudflare 账号（需状态为「已校验」且已确定 account_id）。</Text>
                <Link to="/cf/accounts">前往「Cloudflare 账号」页配置 / 校验</Link>
              </Space>
            }
          />
        </Card>
      ) : (
        <Row gutter={16} style={{ minHeight: 560 }}>
          {/* 左栏：隧道列表 */}
          <Col xs={24} md={8} style={{ display: 'flex', flexDirection: 'column' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
              <Title level={5} style={{ margin: 0 }}>隧道</Title>
              <Space>
                <Button size="small" icon={<ReloadOutlined />} onClick={() => loadTunnels(aid)} loading={tunnelsLoading} />
                <Button size="small" type="primary" icon={<PlusOutlined />} onClick={() => { setNewName(''); setCreateOpen(true); }}>
                  新建隧道
                </Button>
              </Space>
            </div>

            <div style={{ flex: 1, overflowY: 'auto', paddingRight: 4 }}>
              {tunnels.length === 0 ? (
                <Card style={{ textAlign: 'center', padding: '32px 0', borderRadius: 10 }}>
                  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该账号下暂无隧道，点击「新建隧道」创建。" />
                </Card>
              ) : (
                tunnels.map((t) => {
                  const isActive = t.id === tid;
                  return (
                    <Card
                      key={t.id}
                      hoverable
                      style={{
                        marginBottom: 12,
                        cursor: 'pointer',
                        border: `1px solid ${isActive ? token.colorPrimary : token.colorBorderSecondary}`,
                        background: isActive ? token.colorPrimaryBg : token.colorBgContainer,
                        borderRadius: 10,
                      }}
                      styles={{ body: { padding: 14 } }}
                      onClick={() => setTid(t.id)}
                    >
                      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'start', marginBottom: 6 }}>
                        <Text strong style={{ fontSize: 14 }}>{t.name}</Text>
                        {tunnelStatusTag(t.status)}
                      </div>
                      <div style={{ marginBottom: 8 }}>
                        <Text type="secondary" copyable={{ text: t.id }} style={{ fontSize: 11, fontFamily: 'monospace' }}>
                          {t.id}
                        </Text>
                      </div>
                      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                        <Tag bordered={false} color={t.config_src === 'cloudflare' ? 'blue' : 'default'} style={{ fontSize: 11 }}>
                          {t.config_src === 'cloudflare' ? '远端配置' : t.config_src || '本地配置'}
                        </Tag>
                        <Space size={2}>
                          <Tooltip title="重命名">
                            <Button
                              type="text"
                              size="small"
                              icon={<EditOutlined />}
                              onClick={(e) => {
                                e.stopPropagation();
                                setRenameTarget(t);
                                setRenameValue(t.name);
                                setRenameOpen(true);
                              }}
                            />
                          </Tooltip>
                          <Popconfirm
                            title={`删除隧道「${t.name}」？`}
                            description="将从 Cloudflare 删除该隧道，不可恢复。"
                            okText="删除"
                            okButtonProps={{ danger: true }}
                            cancelText="取消"
                            onConfirm={() => handleDeleteTunnel(t)}
                            onPopupClick={(e) => e.stopPropagation()}
                          >
                            <Button type="text" size="small" danger icon={<DeleteOutlined />} onClick={(e) => e.stopPropagation()} />
                          </Popconfirm>
                        </Space>
                      </div>
                    </Card>
                  );
                })
              )}
            </div>
          </Col>

          {/* 右栏：隧道详情 Tabs */}
          <Col xs={24} md={16}>
            {activeTunnel ? (
              <Card bordered={false} style={{ borderRadius: 10 }} styles={{ body: { padding: 16 } }}>
                <div style={{ marginBottom: 12 }}>
                  <Space size={10} align="center" wrap>
                    <Title level={5} style={{ margin: 0 }}>{activeTunnel.name}</Title>
                    {tunnelStatusTag(activeTunnel.status)}
                  </Space>
                </div>
                <Tabs items={tabItems} destroyInactiveTabPane={false} />
              </Card>
            ) : (
              <Card style={{ height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '100px 0', borderRadius: 10 }}>
                <Empty description="请在左侧选择或创建一个隧道。" />
              </Card>
            )}
          </Col>
        </Row>
      )}

      {/* 新建隧道 */}
      <Modal
        title="新建隧道"
        open={createOpen}
        onOk={handleCreate}
        confirmLoading={creating}
        onCancel={() => setCreateOpen(false)}
        okText="创建"
        cancelText="取消"
        destroyOnClose
      >
        <Input
          placeholder="隧道名称，如 my-tunnel"
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          onPressEnter={handleCreate}
          style={{ marginTop: 8 }}
        />
      </Modal>

      {/* 重命名隧道 */}
      <Modal
        title={`重命名隧道 — ${renameTarget?.name || ''}`}
        open={renameOpen}
        onOk={handleRename}
        confirmLoading={renaming}
        onCancel={() => setRenameOpen(false)}
        okText="保存"
        cancelText="取消"
        destroyOnClose
      >
        <Input
          placeholder="新的隧道名称"
          value={renameValue}
          onChange={(e) => setRenameValue(e.target.value)}
          onPressEnter={handleRename}
          style={{ marginTop: 8 }}
        />
      </Modal>
    </Space>
  );
};

export default CFConsole;
