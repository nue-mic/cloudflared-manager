import { useEffect, useState } from 'react';
import { Card, Descriptions, Typography, Skeleton, Empty, Space, App, Button, Table } from 'antd';
import { CopyOutlined } from '@ant-design/icons';
import { stringify as stringifyYaml } from 'yaml';
import client, { instanceApi } from '../../api/client';
import type { ConfigEnvelope, Projection } from '../../api/types';

const { Text } = Typography;

// 把 camelCase 配置对象渲染成 YAML（空对象 → 空串）。
function cfgToYaml(cfg: Record<string, unknown>): string {
  try {
    const text = stringifyYaml(cfg);
    return text.trim() === '{}' ? '' : text;
  } catch {
    return '';
  }
}

interface Props {
  id: string;
}

/**
 * InstanceConfigView —— 只读展示当前生效配置（camelCase 嵌套，token 已脱敏）
 * 与 cfdflags 投影出的真实 TUNNEL_* env / argv，帮用户看懂"到底用什么参数在跑"。
 */
export default function InstanceConfigView({ id }: Props) {
  const { message } = App.useApp();
  const [yaml, setYaml] = useState('');
  const [proj, setProj] = useState<Projection | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let stop = false;
    setLoading(true);
    Promise.all([client.get<ConfigEnvelope>(`/api/v1/configs/${id}`), instanceApi.projection(id)])
      .then(([envRes, projRes]) => {
        if (stop) return;
        const cfg = { ...(envRes.data.config || {}) } as Record<string, unknown>;
        delete cfg.token; // 已脱敏，不展示
        setYaml(cfgToYaml(cfg));
        setProj(projRes.data);
      })
      .catch(() => {
        if (!stop) message.error('加载配置失败');
      })
      .finally(() => {
        if (!stop) setLoading(false);
      });
    return () => {
      stop = true;
    };
  }, [id, message]);

  const copy = (s: string) => {
    navigator.clipboard?.writeText(s).then(
      () => message.success('已复制'),
      () => message.error('复制失败')
    );
  };

  if (loading) return <Skeleton active />;

  const envRows = proj ? Object.entries(proj.env).sort(([a], [b]) => a.localeCompare(b)).map(([k, v]) => ({ k, v })) : [];

  return (
    <Space direction="vertical" size={14} style={{ width: '100%' }}>
      <Card
        size="small"
        title="当前配置（只读，token 已脱敏）"
        extra={
          <Button size="small" icon={<CopyOutlined />} onClick={() => copy(yaml)} disabled={!yaml}>
            复制
          </Button>
        }
        style={{ borderRadius: 10 }}
      >
        {yaml ? (
          <pre className="terminal-container" style={{ margin: 0, maxHeight: 240, overflow: 'auto' }}>
            {yaml}
          </pre>
        ) : (
          <Empty description="配置为空（仅 token，连接参数全用 cloudflared 默认）" image={Empty.PRESENTED_IMAGE_SIMPLE} />
        )}
      </Card>

      <Card
        size="small"
        title="实际运行参数（cfdflags 投影）"
        extra={
          proj && (
            <Button
              size="small"
              icon={<CopyOutlined />}
              onClick={() => copy(`cloudflared ${proj.argv.join(' ')}\n${envRows.map((r) => `${r.k}=${r.v}`).join('\n')}`)}
            >
              复制
            </Button>
          )
        }
        style={{ borderRadius: 10 }}
      >
        {proj && (
          <Space direction="vertical" size={10} style={{ width: '100%' }}>
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>
                argv
              </Text>
              <pre className="terminal-container" style={{ margin: '4px 0 0', padding: '8px 12px' }}>
                cloudflared {proj.argv.join(' ')}
              </pre>
            </div>
            <Table
              size="small"
              pagination={false}
              rowKey="k"
              columns={[
                { title: '环境变量', dataIndex: 'k', width: '45%', render: (v: string) => <Text code style={{ fontSize: 12 }}>{v}</Text> },
                { title: '值', dataIndex: 'v', render: (v: string) => <Text style={{ fontSize: 12 }}>{v}</Text> },
              ]}
              dataSource={envRows}
            />
            <Descriptions
              size="small"
              column={1}
              items={[
                { key: 'bv', label: 'cloudflared 版本', children: proj.binary_version || '—' },
                { key: 'bp', label: '二进制路径', children: <Text code style={{ fontSize: 12 }}>{proj.binary_path}</Text> },
              ]}
            />
          </Space>
        )}
      </Card>
    </Space>
  );
}
