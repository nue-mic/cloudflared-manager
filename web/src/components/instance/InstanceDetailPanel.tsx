import { useEffect, useState } from 'react';
import { Card, Tabs, Button, Badge, Space, Typography, Popconfirm, Tooltip } from 'antd';
import {
  PlayCircleOutlined,
  StopOutlined,
  ReloadOutlined,
  DeleteOutlined,
  EditOutlined,
  CopyOutlined,
  ProfileOutlined,
  FileTextOutlined,
  LineChartOutlined,
  GlobalOutlined,
  SettingOutlined,
  HistoryOutlined,
  CloudOutlined,
} from '@ant-design/icons';
import { instanceApi } from '../../api/client';
import type { Snapshot, LiveStatus } from '../../api/types';
import InstanceOverview from './InstanceOverview';
import InstanceLogPanel from './InstanceLogPanel';
import InstanceMetrics from './InstanceMetrics';
import InstanceConnections from './InstanceConnections';
import InstanceConfigView from './InstanceConfigView';
import InstanceEvents from './InstanceEvents';
import InstanceCFPanel from './InstanceCFPanel';

const { Title } = Typography;

interface Props {
  snap: Snapshot;
  loading?: boolean;
  onStart: () => void;
  onStop: () => void;
  onReload: () => void;
  onEdit: () => void;
  onDuplicate: () => void;
  onDelete: () => void;
}

type BadgeStatus = 'success' | 'processing' | 'warning' | 'default' | 'error';

function statusBadge(snap: Snapshot, live: LiveStatus | null): { status: BadgeStatus; text: string } {
  switch (snap.state) {
    case 'started':
      if (live?.running && live.ha_connections > 0) return { status: 'success', text: '健康 · 已连边缘' };
      if (live?.running) return { status: 'warning', text: '已启动 · 连接边缘中' };
      return { status: 'processing', text: '运行中' };
    case 'starting':
      return { status: 'processing', text: '启动中' };
    case 'stopping':
      return { status: 'processing', text: '停止中' };
    default:
      return { status: 'default', text: '已停止' };
  }
}

/**
 * InstanceDetailPanel —— 实例右侧详情面板。顶部双层状态徽章 + 常驻操作条，下方
 * 6 个 Tab（总览/日志/指标·连接/配置/Cloudflare/事件）。「指标·连接」合并实时连接
 * 与历史指标两块。集中托管 /live 拉取（供徽章/总览/连接共用），运行中每 5s 轮询。
 * 各 Tab 内容按需懒挂载（antd Tabs 默认行为）。
 */
export default function InstanceDetailPanel({
  snap,
  loading,
  onStart,
  onStop,
  onReload,
  onEdit,
  onDuplicate,
  onDelete,
}: Props) {
  const [live, setLive] = useState<LiveStatus | null>(null);
  const running = snap.state === 'started';

  useEffect(() => {
    let stop = false;
    let timer: number | undefined;
    setLive(null);
    const fetchLive = () => {
      instanceApi
        .live(snap.id)
        .then((r) => {
          if (!stop) setLive(r.data);
        })
        .catch(() => {
          if (!stop) setLive(null);
        });
    };
    fetchLive();
    if (running) timer = window.setInterval(fetchLive, 5000);
    return () => {
      stop = true;
      if (timer) clearInterval(timer);
    };
  }, [snap.id, running]);

  const badge = statusBadge(snap, live);

  const tabItems = [
    {
      key: 'overview',
      label: (<span><ProfileOutlined /> 总览</span>),
      children: <InstanceOverview snap={snap} live={live} />,
    },
    {
      key: 'logs',
      label: (<span><FileTextOutlined /> 日志</span>),
      children: <InstanceLogPanel id={snap.id} height="calc(100vh - 300px)" />,
    },
    {
      key: 'runtime',
      label: (<span><LineChartOutlined /> 指标 · 连接</span>),
      children: (
        <Space direction="vertical" size={18} style={{ width: '100%' }}>
          <div>
            <Title level={5} style={{ marginTop: 0, marginBottom: 12 }}>
              <GlobalOutlined /> 实时连接
            </Title>
            <InstanceConnections live={live} running={running} />
          </div>
          <div>
            <Title level={5} style={{ marginBottom: 12 }}>
              <LineChartOutlined /> 历史指标
            </Title>
            <InstanceMetrics id={snap.id} running={running} />
          </div>
        </Space>
      ),
    },
    {
      key: 'config',
      label: (<span><SettingOutlined /> 配置</span>),
      children: <InstanceConfigView id={snap.id} />,
    },
    {
      key: 'cloudflare',
      label: (<span><CloudOutlined /> Cloudflare</span>),
      children: <InstanceCFPanel id={snap.id} />,
    },
    {
      key: 'events',
      label: (<span><HistoryOutlined /> 事件</span>),
      children: <InstanceEvents id={snap.id} />,
    },
  ];

  return (
    <Card bordered={false} styles={{ body: { padding: 16 } }} style={{ height: '100%', minHeight: 560, borderRadius: 10 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 12, flexWrap: 'wrap', marginBottom: 12 }}>
        <div>
          <Title level={4} style={{ margin: 0 }}>{snap.name || snap.id}</Title>
          <Tooltip title="状态只反映隧道↔Cloudflare 边缘的连接；ingress 回源健康在 CF 控制台查看">
            <span><Badge status={badge.status} text={badge.text} /></span>
          </Tooltip>
        </div>
        <Space wrap>
          {running ? (
            <>
              <Button danger icon={<StopOutlined />} loading={loading} onClick={onStop}>停止</Button>
              <Button icon={<ReloadOutlined />} loading={loading} onClick={onReload}>重启</Button>
            </>
          ) : (
            <Button type="primary" icon={<PlayCircleOutlined />} loading={loading} style={{ background: '#52c41a', borderColor: '#52c41a' }} onClick={onStart}>
              启动
            </Button>
          )}
          <Tooltip title="编辑配置"><Button icon={<EditOutlined />} onClick={onEdit} /></Tooltip>
          <Tooltip title="克隆"><Button icon={<CopyOutlined />} onClick={onDuplicate} /></Tooltip>
          <Popconfirm title={`确定删除「${snap.name || snap.id}」？`} description="删除后不可恢复。" onConfirm={onDelete} okText="删除" okType="danger" cancelText="取消">
            <Button danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      </div>

      <Tabs defaultActiveKey="overview" items={tabItems} destroyInactiveTabPane={false} />
    </Card>
  );
}
