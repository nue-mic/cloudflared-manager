import { useEffect, useState } from 'react';
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
import {
  CheckCircleOutlined, CloseCircleOutlined, FileTextOutlined, ThunderboltOutlined,
  ReadOutlined,
} from '@ant-design/icons';
import { useLocation, useNavigate } from 'react-router-dom';
import client from '../api/client';
import { FULL_EXAMPLE, MINIMAL_EXAMPLE } from './configCatalog';

const { Title, Text, Paragraph } = Typography;

interface ValidateResp {
  valid: boolean;
  errors?: string[];
  warnings?: string[];
}

const ToolsValidate: React.FC = () => {
  const { token } = antdTheme.useToken();
  const { message } = App.useApp();
  const location = useLocation();
  const navigate = useNavigate();

  const [content, setContent] = useState('');
  const [result, setResult] = useState<ValidateResp | null>(null);
  const [loading, setLoading] = useState(false);
  const [fromReference, setFromReference] = useState(false);

  const submit = async (text?: string) => {
    const body = (text ?? content).trim();
    if (!body) {
      message.warning('请粘贴 YAML 配置内容');
      return;
    }
    setLoading(true);
    setResult(null);
    try {
      const resp = await client.post<ValidateResp>('/api/v1/validate', body, {
        headers: { 'Content-Type': 'text/plain' },
      });
      setResult(resp.data);
      if (resp.data.valid) message.success('配置校验通过');
      else message.error('配置存在问题，请查看下方明细');
    } catch (e: unknown) {
      const err = e as { response?: { data?: { error?: { message?: string } } } };
      message.error(err.response?.data?.error?.message || '校验请求失败');
    } finally {
      setLoading(false);
    }
  };

  // 从「配置参考」页带草稿跳转过来：预填并自动校验一次，然后清掉路由 state
  // 避免刷新/返回时重复触发。
  useEffect(() => {
    const incoming = (location.state as { yaml?: string } | null)?.yaml;
    if (incoming && incoming.trim()) {
      setContent(incoming);
      setFromReference(true);
      void submit(incoming);
      navigate('.', { replace: true, state: null });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const insert = (yaml: string, label: string) => {
    setContent(yaml);
    setResult(null);
    setFromReference(false);
    message.success(`已插入${label}`);
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

      {fromReference && (
        <Alert
          type="info"
          showIcon
          icon={<ReadOutlined />}
          message="已从「配置参考」载入草稿并自动校验"
          action={<Button size="small" onClick={() => navigate('/tools/reference')}>返回配置参考</Button>}
        />
      )}

      <Card
        title={
          <Space>
            <Text>配置文本</Text>
            <Tag bordered={false}>YAML</Tag>
          </Space>
        }
        extra={
          <Space wrap>
            <Button size="small" icon={<ReadOutlined />} onClick={() => insert(FULL_EXAMPLE, '完整示例')}>
              插入完整示例
            </Button>
            <Button size="small" onClick={() => insert(MINIMAL_EXAMPLE, '最小示例')}>
              最小示例
            </Button>
            <Button size="small" onClick={() => navigate('/tools/reference')}>
              配置参考
            </Button>
            <Button size="small" onClick={() => { setContent(''); setResult(null); setFromReference(false); }}>
              清空
            </Button>
          </Space>
        }
        styles={{ body: { padding: 16 } }}
        style={{ borderRadius: 10 }}
      >
        <Input.TextArea
          value={content}
          onChange={(e) => { setContent(e.target.value); setFromReference(false); }}
          placeholder="粘贴 cloudflared 隧道 YAML，例如 edge.protocol / logging.logLevel / identity.label …"
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
            onClick={() => submit()}
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
              description={
                <div>
                  <div>该配置可被 cloudflared 正常加载。</div>
                  {(result.warnings ?? []).length > 0 && (
                    <ul style={{ margin: '8px 0 0', paddingLeft: 18 }}>
                      {(result.warnings ?? []).map((w, i) => (
                        <li key={i}><Text type="warning" style={{ fontSize: 12.5 }}>{w}</Text></li>
                      ))}
                    </ul>
                  )}
                </div>
              }
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
