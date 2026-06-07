import { useEffect, useState } from 'react';
import {
  Card,
  Space,
  Typography,
  Button,
  Divider,
  Descriptions,
  Tag,
  App,
  Alert,
  theme as antdTheme,
} from 'antd';
import {
  InfoCircleOutlined,
  GithubOutlined,
  SafetyCertificateOutlined,
  BookOutlined,
  DownloadOutlined,
  ReadOutlined,
} from '@ant-design/icons';
import client from '../api/client';
import UpdateCard from '../components/UpdateCard';
import { fmtDateTime } from '../utils/time';

const { Title, Text, Paragraph } = Typography;

interface VersionResp {
  daemon?: string;
  version?: string;
  build_date?: string;
}

const APP_REPO = 'https://github.com/mia-clark/cloudflared-manager';
const APP_RELEASES = 'https://github.com/mia-clark/cloudflared-manager/releases';
const APP_ISSUES = 'https://github.com/mia-clark/cloudflared-manager/issues';
const APP_DOCS_PATH = '/api/docs/';
const INSTALL_URL_GH = 'https://raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.sh';
const DOCKER_IMAGE = 'ghcr.io/mia-clark/cloudflared-manager:latest';

const About: React.FC = () => {
  const { token } = antdTheme.useToken();
  const { message } = App.useApp();
  const [version, setVersion] = useState<VersionResp>({});

  useEffect(() => {
    client.get<VersionResp>('/api/v1/version').then((r) => setVersion(r.data)).catch(() => undefined);
  }, []);

  const copyText = (s: string) => {
    navigator.clipboard.writeText(s);
    message.success('已复制');
  };

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      {/* Hero Banner */}
      <Card
        styles={{ body: { padding: 0 } }}
        style={{ borderRadius: 12, overflow: 'hidden', border: `1px solid ${token.colorBorderSecondary}` }}
      >
        <div
          style={{
            position: 'relative',
            padding: '32px 28px',
            background: 'linear-gradient(135deg, #1e1b4b 0%, #312e81 35%, #6d28d9 75%, #be185d 100%)',
            color: '#fff',
            overflow: 'hidden',
          }}
        >
          <div style={{ position: 'relative', zIndex: 1 }}>
            <Space size={14} align="center">
              <div
                style={{
                  width: 56, height: 56, borderRadius: 14,
                  background: 'rgba(255,255,255,0.18)',
                  border: '1px solid rgba(255,255,255,0.3)',
                  display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                  backdropFilter: 'blur(10px)',
                }}
              >
                <SafetyCertificateOutlined style={{ fontSize: 30, color: '#fff' }} />
              </div>
              <div>
                <Title level={2} style={{ color: '#fff', margin: 0, fontWeight: 700, letterSpacing: '-0.3px' }}>
                  Cloudflared Manager
                </Title>
                <Text style={{ color: 'rgba(255,255,255,0.85)', fontSize: 13.5 }}>
                  无头多实例 cloudflared 隧道管理面板
                </Text>
              </div>
            </Space>

            <Paragraph
              style={{
                color: 'rgba(255,255,255,0.85)',
                marginTop: 18, marginBottom: 18,
                fontSize: 13.5, lineHeight: 1.75, maxWidth: 760,
              }}
            >
              一个守护进程同时托管 N 份 cloudflared 隧道配置，每份跑在独立 worker 子进程里。提供完整的 REST + WebSocket API、二进制版本管理、历史流量曲线、阈值告警与 webhook 推送。单 Go 二进制（无 cgo）。
            </Paragraph>

            <Space wrap size={[8, 8]}>
              <Tag color="default" style={{ background: 'rgba(255,255,255,0.18)', border: '1px solid rgba(255,255,255,0.3)', color: '#fff', borderRadius: 14, padding: '2px 12px' }}>
                cfdmgrd {version.daemon || version.version || '—'}
              </Tag>
              <Tag color="default" style={{ background: 'rgba(255,255,255,0.18)', border: '1px solid rgba(255,255,255,0.3)', color: '#fff', borderRadius: 14, padding: '2px 12px' }}>
                React 19 · Ant Design 6
              </Tag>
              <Tag color="default" style={{ background: 'rgba(255,255,255,0.18)', border: '1px solid rgba(255,255,255,0.3)', color: '#fff', borderRadius: 14, padding: '2px 12px' }}>
                构建 {fmtDateTime(version.build_date)}
              </Tag>
            </Space>
          </div>
        </div>
      </Card>

      {/* 版本升级 */}
      <UpdateCard />

      {/* 信息卡片 */}
      <Card title={<Space><InfoCircleOutlined /> 项目信息</Space>} style={{ borderRadius: 10 }}>
        <Space wrap size={[8, 8]} style={{ marginBottom: 16 }}>
          <Button icon={<GithubOutlined />} href={APP_REPO} target="_blank" rel="noopener noreferrer" type="primary">
            本项目 · mia-clark/cloudflared-manager
          </Button>
          <Button icon={<DownloadOutlined />} href={APP_RELEASES} target="_blank" rel="noopener noreferrer">
            下载 / Releases
          </Button>
          <Button icon={<BookOutlined />} href={APP_DOCS_PATH} target="_blank" rel="noopener noreferrer">
            在线 API 文档
          </Button>
          <Button icon={<ReadOutlined />} href={`${APP_REPO}#readme`} target="_blank" rel="noopener noreferrer">
            README
          </Button>
          <Button danger href={APP_ISSUES} target="_blank" rel="noopener noreferrer">
            报告 Bug
          </Button>
        </Space>

        <Divider style={{ margin: '16px 0' }} />

        <Descriptions column={{ xs: 1, sm: 2, lg: 3 }} size="small" bordered labelStyle={{ width: 110, background: token.colorFillTertiary }}>
          <Descriptions.Item label="应用名称">
            <Space>
              <SafetyCertificateOutlined style={{ color: token.colorPrimary }} />
              Cloudflared Manager
            </Space>
          </Descriptions.Item>
          <Descriptions.Item label="Daemon 版本">
            <Tag>{version.daemon || version.version || '—'}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="构建时间">{fmtDateTime(version.build_date)}</Descriptions.Item>
          <Descriptions.Item label="前端栈">React 19 · Ant Design 6 · Vite</Descriptions.Item>
          <Descriptions.Item label="实时通道">WebSocket (/api/v1/events)</Descriptions.Item>
        </Descriptions>

        <Divider style={{ margin: '16px 0' }} />

        <Alert
          type="info"
          showIcon
          message="快速安装"
          description={
            <div>
              <div style={{ marginBottom: 8 }}>
                <Text strong>Linux / macOS：</Text>
              </div>
              <div
                style={{
                  background: token.colorFillTertiary,
                  borderRadius: 6, padding: '8px 12px',
                  fontFamily: 'monospace', fontSize: 13,
                  cursor: 'pointer', marginBottom: 8,
                }}
                onClick={() => copyText(`curl -fsSL ${INSTALL_URL_GH} | sh -s -- -y`)}
              >
                {`curl -fsSL ${INSTALL_URL_GH} | sh -s -- -y`}
              </div>
              <div style={{ marginBottom: 8 }}>
                <Text strong>Docker：</Text>
              </div>
              <div
                style={{
                  background: token.colorFillTertiary,
                  borderRadius: 6, padding: '8px 12px',
                  fontFamily: 'monospace', fontSize: 13,
                  cursor: 'pointer',
                }}
                onClick={() => copyText(`docker run -d --name cfdmgrd --network host -e CFDM_API_TOKEN="$(openssl rand -hex 32)" -v $(pwd)/cfdmgr-data:/data --restart unless-stopped ${DOCKER_IMAGE}`)}
              >
                {`docker run -d --name cfdmgrd --network host \\`}
                <br />
                {`  -e CFDM_API_TOKEN="$(openssl rand -hex 32)" \\`}
                <br />
                {`  -v $(pwd)/cfdmgr-data:/data --restart unless-stopped \\`}
                <br />
                {`  ${DOCKER_IMAGE}`}
              </div>
              <Text type="secondary" style={{ fontSize: 12, marginTop: 6, display: 'block' }}>
                点击代码块可复制
              </Text>
            </div>
          }
        />
      </Card>
    </Space>
  );
};

export default About;
