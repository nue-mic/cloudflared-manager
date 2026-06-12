import { useEffect, useState } from 'react';
import { Card, Select, Space, Typography, Empty, App } from 'antd';
import { configsApi } from '../api/client';
import type { Snapshot } from '../api/types';
import InstanceLogPanel from '../components/instance/InstanceLogPanel';

const { Title, Text } = Typography;

/**
 * Logs 页 —— 全屏实时日志流。复用 InstanceLogPanel（与 Configs 详情面板同一套
 * 结构化日志组件），本页只负责实例下拉选择 + 页面外框。
 */
const Logs: React.FC = () => {
  const { message } = App.useApp();
  const [instances, setInstances] = useState<Snapshot[]>([]);
  const [selectedId, setSelectedId] = useState<string>('');

  useEffect(() => {
    configsApi
      .list()
      .then((r) => {
        const items = r.data.items ?? [];
        setInstances(items);
        setSelectedId((cur) => cur || items.find((i) => i.state === 'started')?.id || items[0]?.id || '');
      })
      .catch(() => message.error('加载实例列表失败'));
  }, [message]);

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16, flexWrap: 'wrap', gap: 12 }}>
        <Title level={4} style={{ margin: 0 }}>实时日志流监控</Title>
        <Space>
          <Text type="secondary">选择实例：</Text>
          <Select
            value={selectedId || undefined}
            onChange={setSelectedId}
            style={{ width: 260 }}
            placeholder="选择实例"
            options={instances.map((c) => ({
              value: c.id,
              label: (
                <Space>
                  <span
                    style={{
                      width: 6,
                      height: 6,
                      borderRadius: '50%',
                      display: 'inline-block',
                      background: c.state === 'started' ? '#52c41a' : '#d9d9d9',
                    }}
                  />
                  {c.name || c.id}
                  {c.state === 'started' ? ' (运行中)' : ''}
                </Space>
              ),
            }))}
          />
        </Space>
      </div>

      <Card
        styles={{ body: { padding: 16, height: '100%', display: 'flex', flexDirection: 'column' } }}
        style={{ flex: 1, borderRadius: 10, minHeight: 480, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}
      >
        {selectedId ? (
          <InstanceLogPanel id={selectedId} height="100%" />
        ) : (
          <Empty description="请先选择一个 cloudflared 实例" style={{ margin: 'auto' }} />
        )}
      </Card>
    </div>
  );
};

export default Logs;
