import { Row, Col, Card, Statistic, Empty, Tag, Typography, Space } from 'antd';
import { GlobalOutlined, ThunderboltOutlined } from '@ant-design/icons';
import type { LiveStatus } from '../../api/types';

const { Text } = Typography;

interface Props {
  live: LiveStatus | null;
  running: boolean;
}

const PROTO_LABEL: Record<string, string> = { quic: 'QUIC', http2: 'HTTP/2', unknown: '未知' };

export default function InstanceConnections({ live, running }: Props) {
  if (!running || !live?.running) {
    return <Empty description="实例未运行，无边缘连接" style={{ padding: '48px 0' }} />;
  }

  const conns = live.connections ?? [];

  return (
    <Space direction="vertical" size={14} style={{ width: '100%' }}>
      <Row gutter={[12, 12]}>
        <Col xs={12} sm={8}>
          <Card styles={{ body: { padding: 14 } }} style={{ borderRadius: 10 }}>
            <Statistic title="HA 连接数" value={live.ha_connections} valueStyle={{ color: '#52c41a' }} />
          </Card>
        </Col>
        <Col xs={12} sm={8}>
          <Card styles={{ body: { padding: 14 } }} style={{ borderRadius: 10 }}>
            <Statistic
              title="传输协议"
              value={PROTO_LABEL[live.protocol || 'unknown'] ?? live.protocol}
              prefix={<ThunderboltOutlined />}
            />
          </Card>
        </Col>
        <Col xs={24} sm={8}>
          <Card styles={{ body: { padding: 14 } }} style={{ borderRadius: 10 }}>
            <Statistic title="活跃边缘连接" value={conns.length} prefix={<GlobalOutlined />} />
          </Card>
        </Col>
      </Row>

      {conns.length === 0 ? (
        <Empty
          description="暂无 per-connection 指标（部分协议 / cloudflared 版本不暴露）"
          style={{ padding: '32px 0' }}
        />
      ) : (
        <Row gutter={[12, 12]}>
          {conns.map((c) => (
            <Col xs={24} sm={12} lg={8} key={c.conn_index}>
              <Card
                size="small"
                title={
                  <Space>
                    <Tag color="geekblue">#{c.conn_index}</Tag>
                    {c.location ? <Tag color="green">{c.location}</Tag> : <Text type="secondary">colo 未知</Text>}
                  </Space>
                }
                style={{ borderRadius: 10 }}
              >
                <Space direction="vertical" size={2} style={{ width: '100%' }}>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    smoothed RTT：<Text strong>{c.rtt ?? '—'}</Text>
                  </Text>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    丢包：<Text strong>{c.lost_packets ?? 0}</Text>
                  </Text>
                </Space>
              </Card>
            </Col>
          ))}
        </Row>
      )}

      <Text type="secondary" style={{ fontSize: 12 }}>
        RTT 为 cloudflared 原生单位（仅标 RTT，不臆断毫秒）。colo 为边缘数据中心三字码。
      </Text>
    </Space>
  );
}
