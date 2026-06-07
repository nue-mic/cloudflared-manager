import { useState } from 'react';
import {
  Card,
  Space,
  Typography,
  Input,
  Button,
  Alert,
  Tag,
  Divider,
  App,
  theme as antdTheme,
} from 'antd';
import { CheckCircleOutlined, CloseCircleOutlined, FileTextOutlined, ThunderboltOutlined } from '@ant-design/icons';
import client from '../api/client';

const { Title, Text, Paragraph } = Typography;

const SAMPLE_TOML = `tunnel: my-tunnel-id
credentials-file: /etc/cloudflared/my-tunnel.json

ingress:
  - hostname: example.com
    service: http://localhost:8080
  - service: http_status:404
`;

interface ValidateResp {
  valid: boolean;
  errors?: string[];
}

const ToolsValidate: React.FC = () => {
  const { token } = antdTheme.useToken();
  const { message } = App.useApp();
  const [content, setContent] = useState('');
  const [result, setResult] = useState<ValidateResp | null>(null);
  const [loading, setLoading] = useState(false);

  const submit = async () => {
    if (!content.trim()) {
      message.warning('请粘贴 YAML 配置内容');
      return;
    }
    setLoading(true);
    setResult(null);
    try {
      const resp = await client.post<ValidateResp>('/api/v1/validate', content, {
        headers: { 'Content-Type': 'text/plain' },
      });
      setResult(resp.data);
      if (resp.data.valid) message.success('配置校验通过');
      else message.error('配置存在问题，请查看下方明细');
    } catch (e: unknown) {
      const err = e as { response?: { data?: { error?: { message?: string } } } };
      const msg = err.response?.data?.error?.message || '校验请求失败';
      message.error(msg);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card styles={{ body: { padding: 18 } }} style={{ borderRadius: 10 }}>
        <Space direction="vertical" size={4}>
          <Title level={4} style={{ margin: 0 }}>
            <FileTextOutlined /> 配置校验
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            粘贴 cloudflared 隧道的 YAML 配置，由后端解析器完整解析后返回错误明细。不会修改任何持久化数据。
          </Text>
        </Space>
      </Card>

      <Card
        title={
          <Space>
            <Text>配置文本</Text>
            <Tag bordered={false}>YAML</Tag>
          </Space>
        }
        extra={
          <Space>
            <Button size="small" onClick={() => setContent(SAMPLE_TOML)}>
              填入示例
            </Button>
            <Button size="small" onClick={() => { setContent(''); setResult(null); }}>
              清空
            </Button>
          </Space>
        }
        styles={{ body: { padding: 16 } }}
        style={{ borderRadius: 10 }}
      >
        <Input.TextArea
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder="例如：serverAddr = &quot;frp.example.com&quot; ..."
          autoSize={{ minRows: 14, maxRows: 28 }}
          style={{
            fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
            fontSize: 13,
          }}
        />
        <Divider style={{ margin: '16px 0' }} />
        <Space>
          <Button
            type="primary"
            icon={<ThunderboltOutlined />}
            loading={loading}
            onClick={submit}
            disabled={!content.trim()}
          >
            开始校验
          </Button>
          {content && (
            <Text type="secondary" style={{ fontSize: 12 }}>
              共 {content.length.toLocaleString()} 字符 · {content.split('\n').length} 行
            </Text>
          )}
        </Space>
      </Card>

      {result && (
        <Card styles={{ body: { padding: 0 } }} style={{ borderRadius: 10 }}>
          {result.valid ? (
            <Alert
              type="success"
              showIcon
              icon={<CheckCircleOutlined />}
              message="配置完全合法"
              description="该配置可以被 frps 正常加载。"
              style={{ borderRadius: 10 }}
            />
          ) : (
            <Alert
              type="error"
              showIcon
              icon={<CloseCircleOutlined />}
              message={`发现 ${result.errors?.length ?? 1} 个问题`}
              description={
                <ol style={{ margin: 0, paddingLeft: 18 }}>
                  {(result.errors ?? []).map((e, i) => (
                    <li key={i}>
                      <Paragraph
                        copyable
                        style={{ margin: 0, color: token.colorErrorText, fontFamily: 'ui-monospace, monospace', fontSize: 13 }}
                      >
                        {e}
                      </Paragraph>
                    </li>
                  ))}
                </ol>
              }
              style={{ borderRadius: 10 }}
            />
          )}
        </Card>
      )}
    </Space>
  );
};

export default ToolsValidate;
