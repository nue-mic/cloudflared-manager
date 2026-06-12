import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Card,
  Row,
  Col,
  Progress,
  Statistic,
  Space,
  Typography,
  Empty,
  Skeleton,
  List,
  Tag,
  Avatar,
  theme as antdTheme,
} from 'antd';
import {
  DesktopOutlined,
  CloudServerOutlined,
  FieldTimeOutlined,
  DeploymentUnitOutlined,
  ThunderboltOutlined,
  AlertOutlined,
  ClusterOutlined,
} from '@ant-design/icons';
import { AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import { Link } from 'react-router-dom';
import client from '../api/client';
import { useEventStream, useEventSubscription } from '../events/EventStreamContext';
import type { BusEvent, EventType } from '../events/types';
import type { Snapshot } from '../api/types';
import { fmtTime, fmtHourMinute } from '../utils/time';

const { Title, Text } = Typography;

interface SparkPoint {
  t: number;
  cpu: number;
  mem: number;
}

const TYPE_BADGE: Record<EventType, { color: string; label: string }> = {
  'instance.state': { color: 'geekblue', label: '实例状态' },
  'instance.error': { color: 'red', label: '实例错误' },
  'proxy.status': { color: 'cyan', label: '隧道状态' },
  'proxy.connections': { color: 'purple', label: '隧道连接' },
  'config.changed': { color: 'gold', label: '配置变更' },
  'config.deleted': { color: 'volcano', label: '配置删除' },
  'log.line': { color: 'default', label: '日志' },
  alert: { color: 'magenta', label: '告警' },
};

function fmtBytes(n?: number): string {
  if (!n || n <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2)} ${units[i]}`;
}

function fmtUptime(seconds?: number): string {
  if (!seconds || seconds < 0) return '—';
  const d = Math.floor(seconds / 86_400);
  const h = Math.floor((seconds % 86_400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const parts: string[] = [];
  if (d > 0) parts.push(`${d}天`);
  if (h > 0) parts.push(`${h}时`);
  parts.push(`${m}分`);
  return parts.join(' ');
}

function eventSummary(e: BusEvent): string {
  const d = e.data as Record<string, unknown> | undefined;
  if (!d) return '';
  switch (e.type) {
    case 'instance.state':
      return `${d.prev_state ? d.prev_state + ' → ' : ''}${d.state}`;
    case 'instance.error':
      return String(d.message ?? '');
    case 'proxy.status':
      return `[${d.type}] ${d.name} → ${d.status}`;
    case 'proxy.connections':
      return `[${d.type}] ${d.name} 连接数=${d.cur_conns}`;
    case 'log.line':
      return String(d.line ?? '');
    default:
      return JSON.stringify(d);
  }
}

const Dashboard: React.FC = () => {
  const { token } = antdTheme.useToken();
  const { state: connState, lastSeq } = useEventStream();

  const [loading, setLoading] = useState(true);
  const [sysInfo, setSysInfo] = useState<Record<string, any> | null>(null);
  const [history, setHistory] = useState<SparkPoint[]>([]);
  const [configs, setConfigs] = useState<Snapshot[]>([]);
  const [recentEvents, setRecentEvents] = useState<BusEvent[]>([]);
  const prevNet = useRef<{ ts: number; rx: number; tx: number } | null>(null);
  const [netSpeed, setNetSpeed] = useState({ rx: 0, tx: 0 });

  useEventSubscription(null, (e) => {
    setRecentEvents((prev) => {
      const next = prev.length >= 20 ? prev.slice(prev.length - 19) : prev.slice();
      next.push(e);
      return next;
    });
    if (e.type === 'instance.state' || e.type === 'config.changed' || e.type === 'config.deleted') {
      // 实例列表可能改变，刷新一次
      void fetchConfigs();
    }
  });

  const fetchConfigs = async () => {
    try {
      const resp = await client.get('/api/v1/configs');
      if (resp.status === 200) {
        const items = (resp.data?.items ?? []) as Snapshot[];
        setConfigs(items);
      }
    } catch {
      // 静默
    }
  };

  useEffect(() => {
    fetchConfigs();
    let stopped = false;
    const pump = async () => {
      try {
        const resp = await client.get('/api/v1/system/info');
        if (stopped) return;
        const data = resp.data ?? {};
        setSysInfo(data);

        const network = Array.isArray(data.network) ? data.network : [];
        const rxSum = network.reduce((acc: number, n: any) => acc + (n.bytes_recv ?? 0), 0);
        const txSum = network.reduce((acc: number, n: any) => acc + (n.bytes_sent ?? 0), 0);
        const now = Date.now();
        if (prevNet.current) {
          const dt = (now - prevNet.current.ts) / 1000;
          if (dt > 0) {
            setNetSpeed({
              rx: Math.max(0, (rxSum - prevNet.current.rx) / dt),
              tx: Math.max(0, (txSum - prevNet.current.tx) / dt),
            });
          }
        }
        prevNet.current = { ts: now, rx: rxSum, tx: txSum };

        setHistory((prev) => {
          const next = prev.length >= 30 ? prev.slice(prev.length - 29) : prev.slice();
          next.push({
            t: now,
            cpu: data.cpu?.usage_percent ?? 0,
            mem: data.memory?.used_percent ?? 0,
          });
          return next;
        });
      } finally {
        if (!stopped) setLoading(false);
      }
    };
    pump();
    const timer = window.setInterval(pump, 2500);
    return () => {
      stopped = true;
      clearInterval(timer);
    };
  }, []);

  const cpuPercent = Math.round(sysInfo?.cpu?.usage_percent ?? 0);
  const memPercent = Math.round(sysInfo?.memory?.used_percent ?? 0);
  const disks = (sysInfo?.disk ?? []) as Array<{
    path: string;
    fstype?: string;
    used: number;
    total: number;
    used_percent: number;
  }>;
  const mainDisk = disks[0] || { path: '/', used: 0, total: 0, used_percent: 0 };
  const diskPercent = Math.round(mainDisk.used_percent ?? 0);

  const runningCount = configs.filter((c) => c.state === 'started').length;
  const errorCount = configs.filter((c) => !!c.last_error).length;

  const recentReversed = useMemo(() => recentEvents.slice().reverse(), [recentEvents]);

  if (loading) {
    return (
      <Space direction="vertical" size="large" style={{ width: '100%' }}>
        <Skeleton active paragraph={{ rows: 3 }} />
        <Row gutter={[16, 16]}>
          {[0, 1, 2].map((i) => (
            <Col key={i} xs={24} md={8}>
              <Card>
                <Skeleton active avatar paragraph={{ rows: 3 }} />
              </Card>
            </Col>
          ))}
        </Row>
      </Space>
    );
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card styles={{ body: { padding: 18 } }} style={{ borderRadius: 10 }}>
        <Space align="center" style={{ justifyContent: 'space-between', width: '100%' }} wrap>
          <Space direction="vertical" size={2}>
            <Title level={4} style={{ margin: 0 }}>
              仪表盘
            </Title>
            <Text type="secondary" style={{ fontSize: 13 }}>
              一眼掌握 cloudflared 隧道实例、宿主机资源与事件流的实时状态。
            </Text>
          </Space>
          <Space size="middle" wrap>
            <Tag color={connState === 'open' ? 'success' : connState === 'connecting' ? 'warning' : 'error'}>
              {connState === 'open' ? '事件流接通' : connState === 'connecting' ? '连接中…' : '事件流断开'}
            </Tag>
            {lastSeq > 0 && <Tag bordered={false}>seq #{lastSeq}</Tag>}
          </Space>
        </Space>
      </Card>

      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} md={6}>
          <Card styles={{ body: { padding: 18 } }} style={{ borderRadius: 10 }}>
            <Statistic
              title="实例总览"
              value={configs.length}
              prefix={<ClusterOutlined style={{ color: token.colorPrimary, marginRight: 6 }} />}
              suffix="个"
              valueStyle={{ fontSize: 22 }}
            />
            <Space size="small" wrap style={{ marginTop: 8 }}>
              <Tag color="success">运行 {runningCount}</Tag>
              <Tag color="default">停止 {Math.max(0, configs.length - runningCount - errorCount)}</Tag>
              {errorCount > 0 && <Tag color="error">异常 {errorCount}</Tag>}
            </Space>
          </Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card styles={{ body: { padding: 18 } }} style={{ borderRadius: 10 }}>
            <Statistic
              title="主机名"
              value={sysInfo?.host?.hostname || '—'}
              prefix={<DesktopOutlined style={{ color: token.colorSuccess, marginRight: 6 }} />}
              valueStyle={{ fontSize: 18 }}
            />
            <Text type="secondary" style={{ fontSize: 12 }}>
              {sysInfo?.host?.platform} {sysInfo?.host?.platform_version}
            </Text>
          </Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card styles={{ body: { padding: 18 } }} style={{ borderRadius: 10 }}>
            <Statistic
              title="守护进程已运行"
              value={fmtUptime(sysInfo?.uptime_s)}
              prefix={<FieldTimeOutlined style={{ color: token.colorWarning, marginRight: 6 }} />}
              valueStyle={{ fontSize: 18 }}
            />
            <Text type="secondary" style={{ fontSize: 12 }}>
              主机已开机 {fmtUptime(sysInfo?.host?.uptime_seconds)}
            </Text>
          </Card>
        </Col>
        <Col xs={24} sm={12} md={6}>
          <Card styles={{ body: { padding: 18 } }} style={{ borderRadius: 10 }}>
            <Statistic
              title="网络吞吐 ↓"
              value={`${fmtBytes(netSpeed.rx)}/s`}
              prefix={<CloudServerOutlined style={{ color: token.colorInfo, marginRight: 6 }} />}
              valueStyle={{ fontSize: 18 }}
            />
            <Text type="secondary" style={{ fontSize: 12 }}>
              发送 ↑ {fmtBytes(netSpeed.tx)}/s
            </Text>
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]}>
        <Col xs={24} md={8}>
          <Card title={<Space><ThunderboltOutlined /> CPU 使用率</Space>} styles={{ body: { padding: 18, textAlign: 'center' } }} style={{ borderRadius: 10 }}>
            <Progress
              type="dashboard"
              percent={cpuPercent}
              strokeColor={{ '0%': token.colorPrimary, '100%': token.colorSuccess }}
              format={(p) => <Text strong style={{ fontSize: 22 }}>{p}%</Text>}
            />
            <Text type="secondary" style={{ display: 'block', marginTop: 8, fontSize: 12 }}>
              {sysInfo?.cpu?.logical_count ?? '?'} 核 · 物理 {sysInfo?.cpu?.physical_count ?? '?'}
            </Text>
          </Card>
        </Col>
        <Col xs={24} md={8}>
          <Card title="内存 使用率" styles={{ body: { padding: 18, textAlign: 'center' } }} style={{ borderRadius: 10 }}>
            <Progress
              type="dashboard"
              percent={memPercent}
              strokeColor={{ '0%': token.colorSuccess, '100%': token.colorWarning }}
              format={(p) => <Text strong style={{ fontSize: 22 }}>{p}%</Text>}
            />
            <Text type="secondary" style={{ display: 'block', marginTop: 8, fontSize: 12 }}>
              {fmtBytes(sysInfo?.memory?.used)} / {fmtBytes(sysInfo?.memory?.total)}
            </Text>
          </Card>
        </Col>
        <Col xs={24} md={8}>
          <Card title="磁盘 (主分区)" styles={{ body: { padding: 18, textAlign: 'center' } }} style={{ borderRadius: 10 }}>
            <Progress
              type="dashboard"
              percent={diskPercent}
              strokeColor={{ '0%': token.colorWarning, '100%': token.colorError }}
              format={(p) => <Text strong style={{ fontSize: 22 }}>{p}%</Text>}
            />
            <Text type="secondary" style={{ display: 'block', marginTop: 8, fontSize: 12 }}>
              {mainDisk.path} · {fmtBytes(mainDisk.used)} / {fmtBytes(mainDisk.total)}
            </Text>
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]}>
        <Col xs={24} lg={16}>
          <Card title="性能趋势 (近 30 个采样)" styles={{ body: { padding: 18 } }} style={{ borderRadius: 10 }}>
            {history.length > 0 ? (
              <div style={{ width: '100%', height: 240 }}>
                <ResponsiveContainer>
                  <AreaChart data={history} margin={{ top: 4, right: 12, left: -16, bottom: 0 }}>
                    <defs>
                      <linearGradient id="dashCpu" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor={token.colorPrimary} stopOpacity={0.5} />
                        <stop offset="95%" stopColor={token.colorPrimary} stopOpacity={0} />
                      </linearGradient>
                      <linearGradient id="dashMem" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor={token.colorSuccess} stopOpacity={0.5} />
                        <stop offset="95%" stopColor={token.colorSuccess} stopOpacity={0} />
                      </linearGradient>
                    </defs>
                    <CartesianGrid strokeDasharray="3 3" stroke={token.colorBorderSecondary} />
                    <XAxis dataKey="t" tickFormatter={(t) => fmtHourMinute(Number(t))} stroke={token.colorTextSecondary} fontSize={11} />
                    <YAxis domain={[0, 100]} stroke={token.colorTextSecondary} fontSize={11} />
                    <Tooltip
                      contentStyle={{ background: token.colorBgElevated, border: 'none', borderRadius: 8 }}
                      labelFormatter={(t) => fmtTime(Number(t))}
                      formatter={(v, name) => [`${Number(v ?? 0).toFixed(1)}%`, name === 'cpu' ? 'CPU' : '内存']}
                    />
                    <Area type="monotone" dataKey="cpu" stroke={token.colorPrimary} fill="url(#dashCpu)" />
                    <Area type="monotone" dataKey="mem" stroke={token.colorSuccess} fill="url(#dashMem)" />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            ) : (
              <Empty description="收集采样中…" />
            )}
          </Card>
        </Col>

        <Col xs={24} lg={8}>
          <Card
            title={<Space><AlertOutlined /> 实时事件</Space>}
            styles={{ body: { padding: 0 } }}
            style={{ borderRadius: 10 }}
          >
            <List
              size="small"
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="等待事件…" /> }}
              dataSource={recentReversed}
              style={{ maxHeight: 260, overflowY: 'auto' }}
              renderItem={(e) => (
                <List.Item style={{ padding: '8px 14px' }}>
                  <Space direction="vertical" size={0} style={{ width: '100%' }}>
                    <Space size={6}>
                      <Tag bordered={false} color={TYPE_BADGE[e.type]?.color}>
                        {TYPE_BADGE[e.type]?.label ?? e.type}
                      </Tag>
                      <Text type="secondary" style={{ fontSize: 11 }}>
                        {fmtTime(e.ts)}
                      </Text>
                    </Space>
                    <Text style={{ fontSize: 12, fontFamily: 'ui-monospace, monospace' }} ellipsis>
                      {eventSummary(e)}
                    </Text>
                  </Space>
                </List.Item>
              )}
            />
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]}>
        <Col xs={24}>
          <Card
            title={<Space><DeploymentUnitOutlined /> cloudflared 实例</Space>}
            styles={{ body: { padding: 18 } }}
            style={{ borderRadius: 10 }}
            extra={<Link to="/configs">管理实例</Link>}
          >
            {configs.length === 0 ? (
              <Empty description="还没有配置任何 cloudflared 隧道实例" />
            ) : (
              <Row gutter={[12, 12]}>
                {configs.map((c) => (
                  <Col key={c.id} xs={24} sm={12} md={8} lg={6}>
                    <Card size="small" hoverable styles={{ body: { padding: 14 } }} style={{ borderRadius: 8 }}>
                      <Space align="start" size="middle" style={{ width: '100%' }}>
                        <Avatar
                          shape="square"
                          icon={<ClusterOutlined />}
                          style={{
                            background:
                              c.state === 'started'
                                ? token.colorSuccess
                                : c.last_error
                                ? token.colorError
                                : token.colorFillSecondary,
                          }}
                        />
                        <Space direction="vertical" size={2} style={{ flex: 1, minWidth: 0 }}>
                          <Text strong ellipsis style={{ maxWidth: 200 }}>
                            {c.name || c.id.slice(0, 8)}
                          </Text>
                          <Text type="secondary" style={{ fontSize: 12 }} ellipsis>
                            ID: {c.id}
                          </Text>
                          <Tag
                            bordered={false}
                            color={
                              c.state === 'started'
                                ? 'success'
                                : c.last_error
                                ? 'error'
                                : c.state === 'starting' || c.state === 'stopping'
                                ? 'processing'
                                : 'default'
                            }
                            style={{ marginTop: 4 }}
                          >
                            {c.state || 'stopped'}
                          </Tag>
                        </Space>
                      </Space>
                    </Card>
                  </Col>
                ))}
              </Row>
            )}
          </Card>
        </Col>
      </Row>
    </Space>
  );
};

export default Dashboard;
