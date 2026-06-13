// 隧道「Token」Tab：按需拉取 connector token 并展示（敏感凭证提醒）。

import { useState } from 'react';
import { Card, Button, Space, Typography, Alert, App } from 'antd';
import { KeyOutlined, EyeOutlined } from '@ant-design/icons';
import { cfApi } from '../../api/client';

const { Paragraph, Text } = Typography;

interface Props {
  aid: string;
  tid: string;
}

export default function TunnelTokenTab({ aid, tid }: Props) {
  const { message } = App.useApp();
  const [token, setToken] = useState<string>('');
  const [loading, setLoading] = useState(false);

  const errMsg = (err: unknown): string => {
    const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
    return e.response?.data?.error?.message || e.message || '未知错误';
  };

  const reveal = async () => {
    setLoading(true);
    try {
      const resp = await cfApi.tunnelToken(aid, tid);
      setToken(resp.data?.token || '');
    } catch (err: unknown) {
      message.error('获取 token 失败：' + errMsg(err));
    } finally {
      setLoading(false);
    }
  };

  return (
    <Card size="small" style={{ borderRadius: 10 }} title="隧道 Token">
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Alert
          type="warning"
          showIcon
          icon={<KeyOutlined />}
          message="这是敏感凭证"
          description="该 token 可直接用于运行 cloudflared 连接此隧道，泄露等同于隧道被接管。请勿截图、勿外发。"
        />
        {token ? (
          <Paragraph
            copyable={{ text: token }}
            style={{
              margin: 0,
              padding: 12,
              borderRadius: 8,
              wordBreak: 'break-all',
              fontFamily: 'monospace',
              fontSize: 12,
              background: 'rgba(0,0,0,0.04)',
            }}
          >
            {token}
          </Paragraph>
        ) : (
          <Space direction="vertical" size={8}>
            <Text type="secondary">出于安全考虑，token 默认不显示。</Text>
            <Button icon={<EyeOutlined />} loading={loading} onClick={reveal}>
              显示 token
            </Button>
          </Space>
        )}
      </Space>
    </Card>
  );
}
