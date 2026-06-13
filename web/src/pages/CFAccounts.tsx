import { useCallback, useEffect, useState } from 'react';
import {
  Card,
  Table,
  Button,
  Space,
  Typography,
  Tag,
  Modal,
  Form,
  Input,
  Radio,
  Empty,
  Tooltip,
  App,
  theme as antdTheme,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  PlusOutlined,
  ReloadOutlined,
  EditOutlined,
  DeleteOutlined,
  SafetyCertificateOutlined,
  CloudOutlined,
} from '@ant-design/icons';

import { cfApi, type CFAccountInput } from '../api/client';
import type { CFAccountView, CFAccountStatus, CFAccount } from '../api/types';
import { fmtDateTime } from '../utils/time';

const { Title, Text } = Typography;

function statusTag(status: CFAccountStatus) {
  switch (status) {
    case 'active':
      return <Tag color="success">已校验</Tag>;
    case 'invalid':
      return <Tag color="error">校验失败</Tag>;
    default:
      return <Tag>未校验</Tag>;
  }
}

interface AccountFormValues {
  name: string;
  auth_type: 'token' | 'key';
  token?: string;
  email?: string;
  api_key?: string;
  account_id?: string;
}

const CFAccounts: React.FC = () => {
  const { token } = antdTheme.useToken();
  const { message, modal } = App.useApp();
  const [form] = Form.useForm<AccountFormValues>();

  const [items, setItems] = useState<CFAccountView[]>([]);
  const [loading, setLoading] = useState(false);
  const [actionLoading, setActionLoading] = useState<Record<string, boolean>>({});

  // 新建 / 编辑弹窗
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<CFAccountView | null>(null);
  const [saving, setSaving] = useState(false);

  // 二次选择 Cloudflare account 弹窗
  const [pickOpen, setPickOpen] = useState(false);
  const [pickAccounts, setPickAccounts] = useState<CFAccount[]>([]);
  const [pickTargetId, setPickTargetId] = useState<string>('');
  const [pickValue, setPickValue] = useState<string>('');
  const [pickLoading, setPickLoading] = useState(false);

  const authType = Form.useWatch('auth_type', form);

  const errMsg = (err: unknown): string => {
    const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
    return e.response?.data?.error?.message || e.message || '未知错误';
  };

  const loadList = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await cfApi.listAccounts();
      setItems(resp.data?.items || []);
    } catch (err: unknown) {
      message.error('获取账号列表失败：' + errMsg(err));
    } finally {
      setLoading(false);
    }
  }, [message]);

  useEffect(() => {
    loadList();
  }, [loadList]);

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ auth_type: 'token' });
    setModalOpen(true);
  };

  const openEdit = (acc: CFAccountView) => {
    setEditing(acc);
    form.resetFields();
    form.setFieldsValue({
      name: acc.name,
      auth_type: acc.auth_type,
      email: acc.email,
      account_id: acc.account_id,
      token: '',
      api_key: '',
    });
    setModalOpen(true);
  };

  // 校验响应统一处理：失败 warning；成功但需选 account 时弹二次选择。
  const handleVerifyResult = (
    accountId: string,
    resp: { account: CFAccountView; verify: { ok: boolean; error?: string; accounts?: CFAccount[] } }
  ) => {
    const { account, verify } = resp;
    if (!verify.ok) {
      message.warning('校验未通过：' + (verify.error || '账号信息无效，请重新编辑'));
      return;
    }
    // 校验通过但未定 account_id，且有多个候选 → 让用户二次选择。
    if (account.account_id === '' && (verify.accounts?.length ?? 0) > 1) {
      setPickAccounts(verify.accounts || []);
      setPickTargetId(accountId);
      setPickValue(verify.accounts?.[0]?.id || '');
      setPickOpen(true);
      message.info('该 API 凭证可访问多个 Cloudflare account，请选择一个');
      return;
    }
    message.success('账号已校验通过');
  };

  const submit = async () => {
    let values: AccountFormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    setSaving(true);
    try {
      if (editing) {
        // 编辑：secret 留空表示保留原值，不下发。
        const payload: Partial<CFAccountInput> = {
          name: values.name,
          auth_type: values.auth_type,
          account_id: values.account_id || '',
        };
        if (values.auth_type === 'token') {
          if (values.token && values.token.trim()) payload.token = values.token.trim();
        } else {
          payload.email = values.email || '';
          if (values.api_key && values.api_key.trim()) payload.api_key = values.api_key.trim();
        }
        const resp = await cfApi.updateAccount(editing.id, payload);
        setModalOpen(false);
        await loadList();
        handleVerifyResult(editing.id, resp.data);
      } else {
        const payload: CFAccountInput = {
          name: values.name,
          auth_type: values.auth_type,
          account_id: values.account_id || '',
        };
        if (values.auth_type === 'token') {
          payload.token = (values.token || '').trim();
        } else {
          payload.email = values.email || '';
          payload.api_key = (values.api_key || '').trim();
        }
        const resp = await cfApi.createAccount(payload);
        setModalOpen(false);
        await loadList();
        handleVerifyResult(resp.data.account.id, resp.data);
      }
    } catch (err: unknown) {
      message.error('保存失败：' + errMsg(err));
    } finally {
      setSaving(false);
    }
  };

  const confirmPick = async () => {
    if (!pickValue) {
      message.warning('请选择一个 Cloudflare account');
      return;
    }
    setPickLoading(true);
    try {
      await cfApi.updateAccount(pickTargetId, { account_id: pickValue });
      message.success('已绑定 Cloudflare account');
      setPickOpen(false);
      loadList();
    } catch (err: unknown) {
      message.error('绑定失败：' + errMsg(err));
    } finally {
      setPickLoading(false);
    }
  };

  const handleVerify = async (acc: CFAccountView) => {
    setActionLoading((p) => ({ ...p, [acc.id]: true }));
    try {
      const resp = await cfApi.verifyAccount(acc.id);
      await loadList();
      handleVerifyResult(acc.id, resp.data);
    } catch (err: unknown) {
      message.error('校验失败：' + errMsg(err));
    } finally {
      setActionLoading((p) => ({ ...p, [acc.id]: false }));
    }
  };

  const handleDelete = (acc: CFAccountView) => {
    modal.confirm({
      title: `删除账号「${acc.name}」？`,
      content: '删除后，已绑定该账号的实例将失去关联，但不会影响 Cloudflare 上的隧道本身。',
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: async () => {
        try {
          await cfApi.deleteAccount(acc.id);
          message.success('账号已删除');
          loadList();
        } catch (err: unknown) {
          message.error('删除失败：' + errMsg(err));
        }
      },
    });
  };

  const columns: ColumnsType<CFAccountView> = [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
      render: (v: string) => <Text strong>{v}</Text>,
    },
    {
      title: '认证方式',
      dataIndex: 'auth_type',
      key: 'auth_type',
      width: 110,
      render: (v: string) =>
        v === 'token' ? <Tag color="blue">API Token</Tag> : <Tag color="purple">Global Key</Tag>,
    },
    {
      title: 'Cloudflare 账户',
      key: 'account',
      render: (_, r) => (
        <Space direction="vertical" size={0}>
          <Text>{r.account_name || <Text type="secondary">（未确定）</Text>}</Text>
          {r.account_id ? (
            <Text type="secondary" copyable={{ text: r.account_id }} style={{ fontSize: 12, fontFamily: 'monospace' }}>
              {r.account_id}
            </Text>
          ) : (
            <Text type="secondary" style={{ fontSize: 12 }}>account_id 未确定</Text>
          )}
        </Space>
      ),
    },
    {
      title: '邮箱',
      dataIndex: 'email',
      key: 'email',
      render: (v: string) => v || <Text type="secondary">—</Text>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 100,
      render: (v: CFAccountStatus) => statusTag(v),
    },
    {
      title: '最近校验',
      dataIndex: 'last_verified_at',
      key: 'last_verified_at',
      width: 180,
      render: (v: string) => fmtDateTime(v),
    },
    {
      title: '操作',
      key: 'actions',
      width: 150,
      align: 'right',
      render: (_, r) => (
        <Space size={4}>
          <Tooltip title="重新校验">
            <Button
              type="text"
              size="small"
              icon={<SafetyCertificateOutlined />}
              loading={actionLoading[r.id]}
              onClick={() => handleVerify(r)}
            />
          </Tooltip>
          <Tooltip title="编辑">
            <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />
          </Tooltip>
          <Tooltip title="删除">
            <Button type="text" size="small" danger icon={<DeleteOutlined />} onClick={() => handleDelete(r)} />
          </Tooltip>
        </Space>
      ),
    },
  ];

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
        <Space size={12} align="center">
          <Title level={4} style={{ margin: 0 }}>Cloudflare 账号</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            <CloudOutlined style={{ color: token.colorPrimary }} /> 直连 Cloudflare API，管理隧道 / DNS
          </Text>
        </Space>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={loadList} loading={loading}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            新建账号
          </Button>
        </Space>
      </div>

      <Card
        title={
          <Space>
            <CloudOutlined />
            <span>已配置的 Cloudflare 账号</span>
          </Space>
        }
        bordered={false}
        style={{ borderRadius: 10 }}
        styles={{ header: { background: token.colorFillTertiary } }}
      >
        <Table<CFAccountView>
          size="small"
          rowKey="id"
          columns={columns}
          dataSource={items}
          loading={loading}
          pagination={{ pageSize: 10, hideOnSinglePage: true }}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="暂无 Cloudflare 账号，点击「新建账号」接入 API Token 或 Global API Key。"
              />
            ),
          }}
        />
      </Card>

      {/* 新建 / 编辑 Modal */}
      <Modal
        title={editing ? `编辑账号 — ${editing.name}` : '新建 Cloudflare 账号'}
        open={modalOpen}
        onOk={submit}
        confirmLoading={saving}
        onCancel={() => setModalOpen(false)}
        okText="保存并校验"
        cancelText="取消"
        destroyOnClose
        width={560}
      >
        <Form form={form} layout="vertical" requiredMark="optional" style={{ marginTop: 8 }}>
          <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入账号名称' }]}>
            <Input placeholder="如：主账号 / 客户A" />
          </Form.Item>

          <Form.Item label="认证方式" name="auth_type" rules={[{ required: true }]}>
            <Radio.Group>
              <Radio.Button value="token">API Token（推荐）</Radio.Button>
              <Radio.Button value="key">邮箱 + Global API Key</Radio.Button>
            </Radio.Group>
          </Form.Item>

          {authType === 'key' ? (
            <>
              <Form.Item
                label="邮箱"
                name="email"
                rules={[{ required: true, message: '请输入 Cloudflare 账户邮箱' }, { type: 'email', message: '邮箱格式不正确' }]}
              >
                <Input placeholder="you@example.com" />
              </Form.Item>
              <Form.Item
                label="Global API Key"
                name="api_key"
                rules={editing ? [] : [{ required: true, message: '请输入 Global API Key' }]}
                extra={editing && editing.has_key ? '已设置；留空保持不变' : undefined}
              >
                <Input.Password
                  placeholder={editing && editing.has_key ? '留空保持不变' : '粘贴 Global API Key'}
                  autoComplete="new-password"
                />
              </Form.Item>
            </>
          ) : (
            <Form.Item
              label="API Token"
              name="token"
              rules={editing ? [] : [{ required: true, message: '请输入 API Token' }]}
              extra={editing && editing.has_token ? '已设置；留空保持不变' : '在 Cloudflare 控制台「My Profile → API Tokens」创建'}
            >
              <Input.Password
                placeholder={editing && editing.has_token ? '留空保持不变' : '粘贴 API Token'}
                autoComplete="new-password"
              />
            </Form.Item>
          )}

          <Form.Item
            label="Account ID（可选）"
            name="account_id"
            tooltip="多账号时手动指定要操作的 Cloudflare account；留空将自动探测"
          >
            <Input placeholder="留空将自动探测" />
          </Form.Item>
        </Form>
      </Modal>

      {/* 二次选择 Cloudflare account */}
      <Modal
        title="选择 Cloudflare account"
        open={pickOpen}
        onOk={confirmPick}
        confirmLoading={pickLoading}
        onCancel={() => setPickOpen(false)}
        okText="确认绑定"
        cancelText="取消"
        destroyOnClose
      >
        <Space direction="vertical" style={{ width: '100%', marginTop: 8 }} size={12}>
          <Text type="secondary">该 API 凭证可访问以下 {pickAccounts.length} 个 account，请选择本账号要操作的那一个：</Text>
          <Radio.Group
            value={pickValue}
            onChange={(e) => setPickValue(e.target.value)}
            style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
          >
            {pickAccounts.map((a) => (
              <Radio key={a.id} value={a.id}>
                <Space direction="vertical" size={0} style={{ display: 'inline-flex' }}>
                  <Text strong>{a.name}</Text>
                  <Text type="secondary" style={{ fontSize: 12, fontFamily: 'monospace' }}>{a.id}</Text>
                </Space>
              </Radio>
            ))}
          </Radio.Group>
        </Space>
      </Modal>
    </Space>
  );
};

export default CFAccounts;
