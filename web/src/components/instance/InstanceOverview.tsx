import { useEffect, useState } from 'react';
import { Row, Col, Card, Statistic, Alert, Typography, Descriptions, Tag, Space } from 'antd';
import {
  ClusterOutlined,
  ApiOutlined,
  WarningOutlined,
  ClockCircleOutlined,
} from '@ant-design/icons';
import client from '../../api/client';
import type { Snapshot, LiveStatus, AlertEvent } from '../../api/types';
import { fmtDateTime } from '../../utils/time';
import { fmtDuration, fmtBytes, fmtNum } from './format';

const { Text } = Typography;

interface Props {
  snap: Snapshot;
  live: LiveStatus | null;
}

const PROTO_LABEL: Record<string, string> = { quic: 'QUIC', http2: 'HTTP/2', unknown: '—' };

export default function InstanceOverview({ snap, live }: Props) {
  const [firing, setFiring] = useState<AlertEvent[]>([]);
  const [uptime, setUptime] = useState('—');

  // 本实例当前触发中的告警（前端按 inst_id 过滤）。
  useEffect(() => {
    let stop = false;
    client
      .get<{ items: AlertEvent[] }>('/api/v1/alerts/events', { params: { state: 'firing' } })
      .then((r) => {
        if (!stop) setFiring((r.data.items ?? []).filter((e) => e.inst_id === snap.id));
      })
      .catch(() => {});
    return () => {
      stop = true;
    };
  }, [snap.id, snap.state]);

  // 运行时长在副作用里每 10s 跳动（避免在 render 调 Date.now 破坏纯度）。
  useEffect(() => {
    if (snap.state !== 'started' || !snap.started_at) {
      setUptime('—');
      return;
    }
    const start = new Date(snap.started_at).getTime();
    const tick = () => setUptime(fmtDuration(Date.now() - start));
    tick();
    const t = window.setInterval(tick, 10_000);
    return () => clearInterval(t);
  }, [snap.state, snap.started_at]);

  const running = snap.state === 'started';
  const version = live?.version || snap.binary_version || '—';

  return (
    <Space direction="vertical" size={14} style={{ width: '100%' }}>
      {firing.length > 0 && (
        <Alert
          type="error"
          showIcon
          message={`该实例有 ${firing.length} 条告警正在触发`}
          description={firing.map((e) => `${e.rule_id}：当前值 ${fmtNum(Math.round(e.value))}`).join('；')}
        />
      )}
      {snap.last_error && <Alert type="warning" showIcon message="最近错误" description={snap.last_error} />}

      <Row gutter={[12, 12]}>
        <Col xs={12} sm={6}>
          <Card styles={{ body: { padding: 14 } }} style={{ borderRadius: 10 }}>
            <Statistic
              title="HA 连接"
              value={running && live?.running ? live.ha_connections : '—'}
              prefix={<ClusterOutlined />}
              valueStyle={{ fontSize: 22, color: '#52c41a' }}
            />
          </Card>
        </Col>
        <Col xs={12} sm={6}>
          <Card styles={{ body: { padding: 14 } }} style={{ borderRadius: 10 }}>
            <Statistic
              title="累计请求"
              value={live?.running ? fmtNum(live.requests_total) : '—'}
              prefix={<ApiOutlined />}
              valueStyle={{ fontSize: 22 }}
            />
          </Card>
        </Col>
        <Col xs={12} sm={6}>
          <Card styles={{ body: { padding: 14 } }} style={{ borderRadius: 10 }}>
            <Statistic
              title="累计错误"
              value={live?.running ? fmtNum(live.request_errors) : '—'}
              prefix={<WarningOutlined />}
              valueStyle={{ fontSize: 22, color: live?.request_errors ? '#ff4d4f' : undefined }}
            />
          </Card>
        </Col>
        <Col xs={12} sm={6}>
          <Card styles={{ body: { padding: 14 } }} style={{ borderRadius: 10 }}>
            <Statistic
              title="运行时长"
              value={uptime}
              prefix={<ClockCircleOutlined />}
              valueStyle={{ fontSize: 20 }}
            />
          </Card>
        </Col>
      </Row>

      <Descriptions
        bordered
        size="small"
        column={{ xs: 1, sm: 2, lg: 3 }}
        items={[
          { key: 'pid', label: 'PID', children: snap.pid ? snap.pid : <Text type="secondary">未运行</Text> },
          { key: 'ver', label: 'cloudflared 版本', children: version },
          {
            key: 'proto',
            label: '传输协议',
            children: live?.running ? <Tag color="blue">{PROTO_LABEL[live.protocol || 'unknown'] ?? live.protocol}</Tag> : '—',
          },
          { key: 'port', label: 'metrics 端口', children: snap.metrics_port ?? '—' },
          {
            key: 'mem',
            label: 'cloudflared 内存',
            children: live?.running && live.resident_memory_bytes ? fmtBytes(live.resident_memory_bytes) : '—',
          },
          { key: 'gor', label: 'goroutines', children: live?.running ? live.goroutines : '—' },
          { key: 'start', label: '启动时间', children: snap.started_at ? fmtDateTime(snap.started_at) : '—' },
          { key: 'id', label: '实例 ID', children: <Text code>{snap.id}</Text> },
          { key: 'path', label: '配置路径', children: <Text code style={{ fontSize: 12 }}>{snap.path}</Text> },
        ]}
      />

      <Text type="secondary" style={{ fontSize: 12 }}>
        说明：上述状态只反映本连接器与 Cloudflare 边缘的连接（HA 连接、cloudflared 自身指标）；
        ingress / 回源（origin）健康在 Cloudflare Zero Trust 控制台查看。请求 / 错误为计数，非字节带宽。
      </Text>
    </Space>
  );
}
