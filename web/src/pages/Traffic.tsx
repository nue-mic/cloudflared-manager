import { Empty, Card, Space, Typography, Alert } from 'antd';
import { LineChartOutlined } from '@ant-design/icons';

// PR-09b: cloudflared 指标曲线将在下一个 PR 重新接入。
// 旧代码（frps metrics）已注释保留在 git 历史中（ad78730）。

const { Title, Text } = Typography;

const Traffic: React.FC = () => {
  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <Title level={4} style={{ margin: 0 }}>
          <LineChartOutlined /> 历史流量
        </Title>
      </div>

      <Alert
        type="info"
        showIcon
        message="cloudflared 指标曲线接入中（PR-09b）"
        description={
          <Text style={{ fontSize: 13 }}>
            cloudflared 的流量监控指标正在重新接入，将在后续 PR 中提供完整的历史流量曲线功能。
            如需查看当前隧道状态，请前往「cloudflared 实例」页面。
          </Text>
        }
      />

      <Card bordered={false} style={{ borderRadius: 10 }}>
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description="cloudflared 指标曲线接入中（PR-09b）"
        />
      </Card>
    </Space>
  );
};

export default Traffic;
