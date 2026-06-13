// 隧道「连接器」Tab：列出活跃 connector，支持一键清理全部连接。

import { useCallback, useEffect, useState } from 'react';
import { Card, Table, Button, Space, Tag, Empty, Typography, App, Popconfirm } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { ReloadOutlined, ClearOutlined } from '@ant-design/icons';
import { cfApi } from '../../api/client';
import type { CFConnector } from '../../api/types';
import { fmtDateTime } from '../../utils/time';

const { Text } = Typography;

interface Props {
  aid: string;
  tid: string;
}

export default function ConnectorsTab({ aid, tid }: Props) {
  const { message } = App.useApp();
  const [items, setItems] = useState<CFConnector[]>([]);
  const [loading, setLoading] = useState(false);
  const [cleaning, setCleaning] = useState(false);

  const errMsg = (err: unknown): string => {
    const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
    return e.response?.data?.error?.message || e.message || '未知错误';
  };

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await cfApi.listConnections(aid, tid);
      setItems(resp.data?.items || []);
    } catch (err: unknown) {
      message.error('获取连接器失败：' + errMsg(err));
    } finally {
      setLoading(false);
    }
  }, [aid, tid, message]);

  useEffect(() => {
    load();
  }, [load]);

  const cleanup = async () => {
    setCleaning(true);
    try {
      await cfApi.cleanupConnections(aid, tid);
      message.success('已清理全部连接');
      load();
    } catch (err: unknown) {
      message.error('清理失败：' + errMsg(err));
    } finally {
      setCleaning(false);
    }
  };

  const columns: ColumnsType<CFConnector> = [
    {
      title: '连接器 ID',
      dataIndex: 'id',
      key: 'id',
      render: (v: string) => (
        <Text copyable={{ text: v }} style={{ fontSize: 12, fontFamily: 'monospace' }}>{v}</Text>
      ),
    },
    {
      title: '版本',
      dataIndex: 'version',
      key: 'version',
      width: 120,
      render: (v: string) => v || <Text type="secondary">—</Text>,
    },
    {
      title: '架构',
      dataIndex: 'arch',
      key: 'arch',
      width: 110,
      render: (v: string) => (v ? <Tag>{v}</Tag> : <Text type="secondary">—</Text>),
    },
    {
      title: '运行起始',
      dataIndex: 'run_at',
      key: 'run_at',
      width: 180,
      render: (v: string) => fmtDateTime(v),
    },
    {
      title: 'features',
      dataIndex: 'features',
      key: 'features',
      render: (v: string[]) =>
        v && v.length ? (
          <Space size={4} wrap>
            {v.map((f) => (
              <Tag key={f} color="geekblue">{f}</Tag>
            ))}
          </Space>
        ) : (
          <Text type="secondary">—</Text>
        ),
    },
  ];

  return (
    <Card
      size="small"
      style={{ borderRadius: 10 }}
      title="活跃连接器"
      extra={
        <Space>
          <Button size="small" icon={<ReloadOutlined />} onClick={load} loading={loading}>
            刷新
          </Button>
          <Popconfirm
            title="清理全部连接？"
            description="将断开该隧道当前所有 connector 连接，运行中的进程会自动重连。"
            okText="清理"
            okButtonProps={{ danger: true }}
            cancelText="取消"
            onConfirm={cleanup}
          >
            <Button size="small" danger icon={<ClearOutlined />} loading={cleaning}>
              清理全部连接
            </Button>
          </Popconfirm>
        </Space>
      }
    >
      <Table<CFConnector>
        size="small"
        rowKey="id"
        columns={columns}
        dataSource={items}
        loading={loading}
        pagination={false}
        locale={{
          emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无活跃连接器" />,
        }}
      />
    </Card>
  );
}
