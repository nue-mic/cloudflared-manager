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
  Row,
  Col,
  Alert,
  Tabs,
  Table,
  theme as antdTheme,
} from 'antd';
import {
  InfoCircleOutlined,
  GithubOutlined,
  SafetyCertificateOutlined,
  CopyOutlined,
  LinkOutlined,
  BookOutlined,
  CloudServerOutlined,
  RocketOutlined,
  ToolOutlined,
  DownloadOutlined,
  ReadOutlined,
  CloudOutlined,
} from '@ant-design/icons';
import client from '../api/client';
import UpdateCard from '../components/UpdateCard';
import BrandMark from '../components/BrandMark';
import { fmtDateTime } from '../utils/time';

const { Title, Text, Paragraph } = Typography;

interface VersionResp {
  daemon?: string;
  version?: string;
  build_date?: string;
}

const APP_REPO = 'https://github.com/nue-mic/cloudflared-manager';
const UPSTREAM_CFD_REPO = 'https://github.com/cloudflare/cloudflared';
const ZERO_TRUST_DASH = 'https://one.dash.cloudflare.com/';
const APP_RELEASES = 'https://github.com/nue-mic/cloudflared-manager/releases';
const APP_ISSUES = 'https://github.com/nue-mic/cloudflared-manager/issues';
const APP_DOCS_PATH = '/api/docs/';

// 国内加速：自建 gh-raw 代理，安装脚本配置键 (key) = cfd-mgr
const INSTALL_URL_CN = 'https://gh-raw.966788.xyz/cfd-mgr/install.sh';
const INSTALL_URL_PS1_CN = 'https://gh-raw.966788.xyz/cfd-mgr/install.ps1';
const INSTALL_URL_GH = 'https://raw.githubusercontent.com/nue-mic/cloudflared-manager/main/scripts/install.sh';
const DOCKER_IMAGE = 'ghcr.io/nue-mic/cloudflared-manager:latest';

const About: React.FC = () => {
  const { token } = antdTheme.useToken();
  const { message } = App.useApp();
  const [version, setVersion] = useState<VersionResp>({});

  useEffect(() => {
    client.get<VersionResp>('/api/v1/version').then((r) => setVersion(r.data)).catch(() => undefined);
  }, []);

  const copyText = (s: string) => {
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(s).then(
        () => message.success('已复制'),
        () => message.error('复制失败'),
      );
    } else {
      const ta = document.createElement('textarea');
      ta.value = s;
      ta.style.position = 'fixed';
      ta.style.left = '-9999px';
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
      message.success('已复制');
    }
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
          <div
            aria-hidden
            style={{
              position: 'absolute', top: '-30%', right: '-10%', width: 320, height: 320,
              borderRadius: '50%',
              background: 'radial-gradient(circle, rgba(190,24,93,0.35) 0%, transparent 70%)',
              filter: 'blur(20px)', pointerEvents: 'none',
            }}
          />
          <div
            aria-hidden
            style={{
              position: 'absolute', bottom: '-40%', left: '5%', width: 280, height: 280,
              borderRadius: '50%',
              background: 'radial-gradient(circle, rgba(129,140,248,0.32) 0%, transparent 70%)',
              filter: 'blur(20px)', pointerEvents: 'none',
            }}
          />

          <div style={{ position: 'relative', zIndex: 1 }}>
            <Space size={14} align="center">
              <div
                style={{
                  width: 56, height: 56, borderRadius: 14,
                  background: 'rgba(255,255,255,0.18)',
                  border: '1px solid rgba(255,255,255,0.3)',
                  display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                  backdropFilter: 'blur(10px)', WebkitBackdropFilter: 'blur(10px)',
                }}
              >
                <BrandMark size={32} color="#fff" />
              </div>
              <div>
                <Title level={2} style={{ color: '#fff', margin: 0, fontWeight: 700, letterSpacing: '-0.3px' }}>
                  Cloudflared 隧道管理器
                </Title>
                <Text style={{ color: 'rgba(255,255,255,0.85)', fontSize: 13.5 }}>
                  Cloudflare Tunnel · 多实例 token 模式管理面板
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
              一个守护进程同时托管 N 份 cloudflared 隧道配置，每份跑在独立 worker 子进程里。提供完整的 REST + WebSocket API、二进制版本管理、历史流量曲线、阈值告警与 webhook 推送。cloudflared 二进制由面板下载并多版本并存。单 Go 二进制（无 cgo）。
            </Paragraph>

            <Space wrap size={[8, 8]}>
              <Tag style={{ background: 'rgba(255,255,255,0.18)', border: '1px solid rgba(255,255,255,0.3)', color: '#fff', borderRadius: 14, padding: '2px 12px' }}>
                cfdmgrd {version.daemon || version.version || '—'}
              </Tag>
              <Tag style={{ background: 'rgba(255,255,255,0.18)', border: '1px solid rgba(255,255,255,0.3)', color: '#fff', borderRadius: 14, padding: '2px 12px' }}>
                React 19 · Ant Design 6
              </Tag>
              <Tag style={{ background: 'rgba(255,255,255,0.18)', border: '1px solid rgba(255,255,255,0.3)', color: '#fff', borderRadius: 14, padding: '2px 12px' }}>
                构建 {fmtDateTime(version.build_date)}
              </Tag>
            </Space>
          </div>
        </div>
      </Card>

      {/* 版本升级 —— 检查最新版 / 更新日志 / 一键更新 */}
      <UpdateCard />

      {/* 5 Tab 主体 */}
      <Card
        title={<Space><InfoCircleOutlined /> 使用手册 & 部署指南</Space>}
        styles={{ body: { padding: 0 } }}
        style={{ borderRadius: 10, overflow: 'hidden' }}
      >
        <Tabs
          defaultActiveKey="info"
          tabBarStyle={{ padding: '0 18px', marginBottom: 0 }}
          items={[
            {
              key: 'info',
              label: <Space size={6}><LinkOutlined />相关链接</Space>,
              children: renderInfoTab({ token, version }),
            },
            {
              key: 'install',
              label: <Space size={6}><RocketOutlined />快速部署</Space>,
              children: renderInstallTab({ token, copyText }),
            },
            {
              key: 'docker',
              label: <Space size={6}><CloudServerOutlined />Docker</Space>,
              children: renderDockerTab({ token, copyText }),
            },
            {
              key: 'cfm',
              label: <Space size={6}><ToolOutlined />cfm 命令</Space>,
              children: renderCfmTab({ token, copyText }),
            },
            {
              key: 'env',
              label: <Space size={6}><BookOutlined />环境变量</Space>,
              children: renderEnvTab({ token }),
            },
          ]}
        />
      </Card>
    </Space>
  );
};

export default About;

// ============================================================================
// Tab 渲染辅助
// ============================================================================

type TokenLike = ReturnType<typeof antdTheme.useToken>['token'];

/** 带复制按钮的代码块。 */
const CodeBlock: React.FC<{
  code: string;
  token: TokenLike;
  onCopy: (text: string) => void;
  language?: string;
}> = ({ code, token, onCopy, language }) => (
  <div
    style={{
      position: 'relative',
      background: token.colorFillTertiary,
      border: `1px solid ${token.colorBorderSecondary}`,
      borderRadius: 8,
      padding: '14px 44px 14px 14px',
      fontFamily: "'Cascadia Mono', Consolas, 'SF Mono', Menlo, monospace",
      fontSize: 12.5,
      lineHeight: 1.7,
      overflowX: 'auto',
      marginBottom: 12,
      color: token.colorText,
    }}
  >
    {language && (
      <Tag style={{ position: 'absolute', top: 8, left: 12, fontSize: 10.5, opacity: 0.65 }}>
        {language}
      </Tag>
    )}
    <Button
      type="text"
      size="small"
      icon={<CopyOutlined />}
      title="复制到剪贴板"
      style={{ position: 'absolute', top: 6, right: 6 }}
      onClick={() => onCopy(code)}
    />
    <pre style={{ margin: language ? '14px 0 0' : 0, padding: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{code}</pre>
  </div>
);

const SectionTitle: React.FC<{ children: React.ReactNode; icon?: React.ReactNode }> = ({ children, icon }) => (
  <Title level={5} style={{ marginTop: 18, marginBottom: 10 }}>
    <Space size={6}>{icon}{children}</Space>
  </Title>
);

// ---- 1. 相关链接 ----
function renderInfoTab(opts: { token: TokenLike; version: VersionResp }) {
  const { token, version } = opts;
  return (
    <div style={{ padding: 18 }}>
      <Alert
        type="info"
        showIcon
        message="3 句话上手"
        description={
          <span style={{ fontSize: 12.5 }}>
            ① 在 Cloudflare Zero Trust 控制台建好 Tunnel（ingress / 公共主机名在那边管），拿到 <Text strong>隧道 token</Text>。
            ② 用下方「快速部署」一键装上 <Text code>cfdmgrd</Text> 守护进程 → 浏览器开 <Text code>http://IP:端口/</Text> → 填 API 令牌登录。
            ③ 网页上新建实例、粘贴 token、点启动；本面板只管「用哪个 token 跑、用什么连接参数跑、跑没跑」。
          </span>
        }
        style={{ marginBottom: 18 }}
      />

      <SectionTitle icon={<LinkOutlined />}>相关链接</SectionTitle>
      <Space wrap size={[8, 8]}>
        <Button icon={<GithubOutlined />} href={APP_REPO} target="_blank" rel="noopener noreferrer" type="primary">
          本项目 · nue-mic/cloudflared-manager
        </Button>
        <Button icon={<CloudOutlined />} href={ZERO_TRUST_DASH} target="_blank" rel="noopener noreferrer">
          Cloudflare Zero Trust 控制台
        </Button>
        <Button icon={<GithubOutlined />} href={UPSTREAM_CFD_REPO} target="_blank" rel="noopener noreferrer">
          上游 · cloudflare/cloudflared
        </Button>
        <Button icon={<DownloadOutlined />} href={APP_RELEASES} target="_blank" rel="noopener noreferrer">
          下载 / Releases
        </Button>
        <Button icon={<BookOutlined />} href={APP_DOCS_PATH} target="_blank" rel="noopener noreferrer">
          在线 API 文档 (本机 Scalar)
        </Button>
        <Button icon={<ReadOutlined />} href={`${APP_REPO}#readme`} target="_blank" rel="noopener noreferrer">
          README 使用指南
        </Button>
        <Button danger href={APP_ISSUES} target="_blank" rel="noopener noreferrer">
          报告 Bug / 提建议
        </Button>
      </Space>

      <Divider style={{ margin: '24px 0 16px' }} />

      <SectionTitle>构建详情</SectionTitle>
      <Descriptions column={{ xs: 1, sm: 2, lg: 3 }} size="small" bordered labelStyle={{ width: 110, background: token.colorFillTertiary }}>
        <Descriptions.Item label="应用名称">
          <Space>
            <SafetyCertificateOutlined style={{ color: token.colorPrimary }} />
            Cloudflared 隧道管理器
          </Space>
        </Descriptions.Item>
        <Descriptions.Item label="Daemon 版本">
          <Tag>{version.daemon || version.version || '—'}</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="cloudflared">
          <Tag color="orange">下载 / 多版本管理</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="构建时间">{fmtDateTime(version.build_date)}</Descriptions.Item>
        <Descriptions.Item label="前端栈">React 19 · Ant Design 6 · Vite</Descriptions.Item>
        <Descriptions.Item label="实时通道">WebSocket (/api/v1/events)</Descriptions.Item>
      </Descriptions>
    </div>
  );
}

// ---- 2. 快速部署 ----
function renderInstallTab(opts: { token: TokenLike; copyText: (s: string) => void }) {
  const { token, copyText } = opts;

  const cnInteractive = `curl -fsSL ${INSTALL_URL_CN} | sh`;
  const cnAuto = `curl -fsSL ${INSTALL_URL_CN} | sh -s -- -y`;
  const cnCustom = `curl -fsSL ${INSTALL_URL_CN} | sh -s -- -y -p 9000 -t 我的强随机令牌`;
  const ghAuto = `curl -fsSL ${INSTALL_URL_GH} | sh -s -- -y`;
  const winInstall = `irm ${INSTALL_URL_PS1_CN} | iex`;
  const winCustom = `$env:CFDM_PORT=9000; $env:CFDM_API_TOKEN='我的强随机令牌'; $env:ASSUME_YES=1; irm ${INSTALL_URL_PS1_CN} | iex`;
  const cnUpdate = `curl -fsSL ${INSTALL_URL_CN} | sh -s -- --update --force`;
  const cnUninstall = `curl -fsSL ${INSTALL_URL_CN} | sh -s -- --uninstall`;
  const linuxFull = `curl -fsSL ${INSTALL_URL_CN} | sh -s -- -y -p 9000 -t 我的强随机令牌`;
  const manualBin = `# 1. 到 Releases 下载对应平台压缩包
curl -LO https://github.com/nue-mic/cloudflared-manager/releases/latest/download/cfdmgrd_linux_amd64.tar.gz

# 2. 解压
tar -xzf cfdmgrd_linux_amd64.tar.gz

# 3. 启动（设个强随机 token）
CFDM_API_TOKEN=$(openssl rand -hex 32) ./cfdmgrd serve`;

  return (
    <div style={{ padding: 18 }}>
      <Alert
        type="info"
        showIcon
        message="一键安装支持 Linux / macOS / FreeBSD / Windows，自动识别系统与 CPU 架构，安装并注册成系统服务（systemd / OpenRC / launchd / Windows 服务），生成统一管理命令 cfm。"
        style={{ marginBottom: 16 }}
      />
      <Alert
        type="success"
        showIcon
        message="智能下载（无需手动配代理）"
        description={
          <span style={{ fontSize: 12.5 }}>
            脚本内置 7 家自建 gh-raw release 代理（配置键 <Text code>cfd-mgr</Text>），下载二进制时按优先级挨个尝试，
            第一个能完整下载并通过 <Text code>tar -tzf</Text> / <Text code>Expand-Archive</Text> 校验的就用；全部代理失败才回落 GitHub 直连。
            需要强制指定时：<Text code>--proxy https://my.mirror/</Text> 或环境变量 <Text code>CFDM_DOWNLOAD_PROXY</Text>；
            跳过代理：<Text code>--no-proxy</Text> / <Text code>CFDM_NO_PROXY=1</Text>。
          </span>
        }
        style={{ marginBottom: 16 }}
      />

      <SectionTitle icon={<RocketOutlined />}>一键安装（国内镜像加速 · 推荐）</SectionTitle>
      <Paragraph type="secondary" style={{ marginBottom: 8, fontSize: 12.5 }}>
        交互式安装（逐步问端口和令牌）：
      </Paragraph>
      <CodeBlock code={cnInteractive} token={token} onCopy={copyText} language="sh" />

      <Paragraph type="secondary" style={{ marginBottom: 8, fontSize: 12.5 }}>
        全自动安装（默认端口 8080，自动生成随机令牌）：
      </Paragraph>
      <CodeBlock code={cnAuto} token={token} onCopy={copyText} language="sh" />

      <Paragraph type="secondary" style={{ marginBottom: 8, fontSize: 12.5 }}>
        指定端口 + 令牌（零交互）：
      </Paragraph>
      <CodeBlock code={cnCustom} token={token} onCopy={copyText} language="sh" />

      <SectionTitle icon={<GithubOutlined />}>海外服务器 / GitHub 直连</SectionTitle>
      <CodeBlock code={ghAuto} token={token} onCopy={copyText} language="sh" />

      <SectionTitle icon={<CloudOutlined />}>Windows（管理员 PowerShell）</SectionTitle>
      <Paragraph type="secondary" style={{ marginBottom: 8, fontSize: 12.5 }}>
        交互安装（逐步问端口和令牌）：
      </Paragraph>
      <CodeBlock code={winInstall} token={token} onCopy={copyText} language="powershell" />
      <Paragraph type="secondary" style={{ marginBottom: 8, fontSize: 12.5 }}>
        全自动 + 指定端口 + 令牌（PowerShell 用环境变量代替命令行参数）：
      </Paragraph>
      <CodeBlock code={winCustom} token={token} onCopy={copyText} language="powershell" />

      <Alert
        type="info"
        showIcon
        message="完整三系统对照（指定端口 + 令牌，全自动一键复制）"
        description={
          <div style={{ fontSize: 12.5 }}>
            <div style={{ marginTop: 8 }}>
              <Text strong>Linux</Text>（systemd / OpenRC，开机自启）：
            </div>
            <CodeBlock code={linuxFull} token={token} onCopy={copyText} language="sh" />
            <div>
              <Text strong>macOS</Text>（launchd，开机自启）—— 与 Linux 同脚本：
            </div>
            <CodeBlock code={linuxFull} token={token} onCopy={copyText} language="sh" />
            <div>
              <Text strong>Windows</Text>（NSSM 包装 Windows 服务，<Text type="warning">需管理员 PowerShell</Text>）：
            </div>
            <CodeBlock code={winCustom} token={token} onCopy={copyText} language="powershell" />
            <div style={{ marginTop: 4, opacity: 0.7 }}>
              把 <Text code>9000</Text> 和 <Text code>我的强随机令牌</Text> 改成你想要的值；三套都装完用统一的 <Text code>cfm start/status/info</Text> 运维。
            </div>
          </div>
        }
        style={{ marginTop: 14, marginBottom: 14 }}
      />

      <SectionTitle>升级到最新版（保留端口/令牌/数据）</SectionTitle>
      <CodeBlock code={cnUpdate} token={token} onCopy={copyText} language="sh" />

      <SectionTitle>卸载</SectionTitle>
      <CodeBlock code={cnUninstall} token={token} onCopy={copyText} language="sh" />

      <SectionTitle icon={<DownloadOutlined />}>手动下载二进制（任何系统）</SectionTitle>
      <Paragraph type="secondary" style={{ marginBottom: 8, fontSize: 12.5 }}>
        支持 Linux (amd64/arm64/armv6/v7/386/riscv64/mips/loong64) / macOS (amd64/arm64) / Windows (amd64/arm64/386) / FreeBSD (amd64/arm64)。
      </Paragraph>
      <CodeBlock code={manualBin} token={token} onCopy={copyText} language="sh" />
    </div>
  );
}

// ---- 3. Docker ----
function renderDockerTab(opts: { token: TokenLike; copyText: (s: string) => void }) {
  const { token, copyText } = opts;

  const dockerRun = `docker run -d --name cfdmgrd --network host \\
  -e CFDM_API_TOKEN="$(openssl rand -hex 32)" \\
  -e CFDM_HTTP_ADDR=":8080" \\
  -v $(pwd)/cfdmgr-data:/data \\
  --restart unless-stopped \\
  ${DOCKER_IMAGE}`;

  const composeStandalone = `# docker-compose.yml
services:
  cfdmgrd:
    image: ${DOCKER_IMAGE}
    container_name: cfdmgrd
    # host 模式让 cloudflared 子进程直接走宿主机网络（推荐）
    network_mode: host
    restart: unless-stopped
    environment:
      # ⚠️ 必填，登录管理面板的 Bearer 令牌（强随机字符串）
      CFDM_API_TOKEN: \${CFDM_API_TOKEN:?required}
      CFDM_HTTP_ADDR: ":8080"
      CFDM_DATA_DIR: "/data"
      CFDM_LOG_LEVEL: "info"
      CFDM_DOCS_ENABLED: "true"
      TZ: "Asia/Shanghai"
    volumes:
      - ./cfdmgr-data:/data`;

  const envExample = `# .env
# 生成强随机令牌:  openssl rand -hex 32
CFDM_API_TOKEN=change-me-to-a-real-strong-token`;

  const composeUp = `docker compose up -d`;
  const composeLogs = `docker compose logs -f`;
  const composePull = `docker compose pull && docker compose up -d`;
  const composeDown = `docker compose down`;

  const standaloneFetch = `# 一行下载现成 compose 与 .env 模板
mkdir cfdmgrd && cd cfdmgrd
curl -fsSL https://raw.githubusercontent.com/nue-mic/cloudflared-manager/main/deploy/docker-compose.standalone.yml -o docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/nue-mic/cloudflared-manager/main/deploy/.env.example -o .env

# 改 .env: 填入 CFDM_API_TOKEN
docker compose up -d`;

  return (
    <div style={{ padding: 18 }}>
      <Alert
        type="info"
        showIcon
        message={
          <Space wrap>
            <span>多架构镜像：</span>
            <Tag color="blue">linux/amd64</Tag>
            <Tag color="blue">linux/arm64</Tag>
            <Text code copyable={{ text: DOCKER_IMAGE, onCopy: () => copyText(DOCKER_IMAGE) }}>{DOCKER_IMAGE}</Text>
          </Space>
        }
        description={<span style={{ fontSize: 12 }}>每次推送到 main 与每个发布 tag 自动构建。推荐 host 网络让 cloudflared 子进程直连边缘，省一层 NAT。数据挂卷到容器内 <Text code>/data</Text>。</span>}
        style={{ marginBottom: 16 }}
      />

      <SectionTitle icon={<CloudServerOutlined />}>方式一 · docker run 单条命令</SectionTitle>
      <CodeBlock code={dockerRun} token={token} onCopy={copyText} language="sh" />

      <SectionTitle>方式二 · docker compose 模板（推荐生产）</SectionTitle>
      <Paragraph type="secondary" style={{ marginBottom: 8, fontSize: 12.5 }}>
        把下面整段保存为 <Text code>docker-compose.yml</Text>：
      </Paragraph>
      <CodeBlock code={composeStandalone} token={token} onCopy={copyText} language="yaml" />
      <Paragraph type="secondary" style={{ marginBottom: 8, fontSize: 12.5 }}>
        同目录创建 <Text code>.env</Text>：
      </Paragraph>
      <CodeBlock code={envExample} token={token} onCopy={copyText} language="ini" />

      <SectionTitle>方式三 · 一键拉模板</SectionTitle>
      <CodeBlock code={standaloneFetch} token={token} onCopy={copyText} language="sh" />

      <SectionTitle icon={<ToolOutlined />}>常用运维命令</SectionTitle>
      <Row gutter={[12, 12]}>
        <Col xs={24} md={12}>
          <Text style={{ fontSize: 12.5 }}>启动 / 创建：</Text>
          <CodeBlock code={composeUp} token={token} onCopy={copyText} language="sh" />
        </Col>
        <Col xs={24} md={12}>
          <Text style={{ fontSize: 12.5 }}>查日志：</Text>
          <CodeBlock code={composeLogs} token={token} onCopy={copyText} language="sh" />
        </Col>
        <Col xs={24} md={12}>
          <Text style={{ fontSize: 12.5 }}>升级到最新镜像：</Text>
          <CodeBlock code={composePull} token={token} onCopy={copyText} language="sh" />
        </Col>
        <Col xs={24} md={12}>
          <Text style={{ fontSize: 12.5 }}>停止 / 移除：</Text>
          <CodeBlock code={composeDown} token={token} onCopy={copyText} language="sh" />
        </Col>
      </Row>

      <Alert
        type="warning"
        showIcon
        style={{ marginTop: 16 }}
        message="数据持久化"
        description={
          <span style={{ fontSize: 12.5 }}>
            升级、重装时只要保留挂载的 <Text code>./cfdmgr-data</Text> 卷（容器内 <Text code>/data</Text>），所有隧道配置（<Text code>profiles/</Text>）、日志（<Text code>logs/</Text>）、cloudflared 二进制（<Text code>bin/</Text>）、指标库（<Text code>metrics.db</Text>）、实例元数据（<Text code>meta.json</Text>）都不会丢。
          </span>
        }
      />
    </div>
  );
}

// ---- 4. cfm 命令 ----
function renderCfmTab(opts: { token: TokenLike; copyText: (s: string) => void }) {
  const { token, copyText } = opts;

  const cfmTable: Array<{ category: string; cmd: string; desc: string }> = [
    { category: '服务管理', cmd: 'cfm start',     desc: '启动 cfdmgrd 守护进程' },
    { category: '服务管理', cmd: 'cfm stop',      desc: '停止守护进程' },
    { category: '服务管理', cmd: 'cfm restart',   desc: '重启守护进程（stop + start）' },
    { category: '服务管理', cmd: 'cfm status',    desc: '查看运行状态（PID / 活跃 / 退出码）' },
    { category: '服务管理', cmd: 'cfm logs -f',   desc: '查看守护进程日志，-f 实时跟随' },
    { category: '服务管理', cmd: 'cfm enable',    desc: '设置开机自启' },
    { category: '服务管理', cmd: 'cfm disable',   desc: '取消开机自启' },
    { category: '信息查看', cmd: 'cfm info',      desc: '⭐ 显示监听地址 / 数据目录 / API 令牌 — 忘了令牌看这个' },
    { category: '信息查看', cmd: 'cfm version',   desc: '显示守护进程版本与构建信息' },
    { category: '信息查看', cmd: 'cfm config',    desc: '查看配置文件（config edit 用编辑器打开）' },
    { category: '安装维护', cmd: 'cfm install',   desc: '安装 / 重装（--version=X 指定版本，省略=latest）' },
    { category: '安装维护', cmd: 'cfm update',    desc: '升级到指定版本（省略=latest），保留配置与数据' },
    { category: '安装维护', cmd: 'cfm uninstall', desc: '卸载（--purge 同时删除数据目录）' },
    { category: '进阶辅助', cmd: 'cfm doctor',    desc: '🩺 8 项健康自检（进程/端口/令牌/二进制/数据目录/DNS/CF API/代理）' },
    { category: '进阶辅助', cmd: 'cfm backup',    desc: '打包 meta.json / profiles / metrics.db 为 tar.gz（--include-logs 含日志）' },
    { category: '进阶辅助', cmd: 'cfm restore',   desc: '从备份恢复（已有数据需 --force 才覆盖）' },
    { category: '进阶辅助', cmd: 'cfm watch',     desc: '终端实时面板（btop 风格，--interval=N 设刷新秒数，q 退出）' },
    { category: '帮助',     cmd: 'cfm help',      desc: '列出全部子命令与简要说明' },
  ];

  return (
    <div style={{ padding: 18 }}>
      <Alert
        type="info"
        showIcon
        message="cfm 是一键安装脚本生成的统一管理命令（已加入 PATH），自动适配 systemd / OpenRC / launchd / Windows 服务。三端一致，共 18 个子命令。"
        style={{ marginBottom: 16 }}
      />
      <Table
        size="small"
        pagination={false}
        rowKey="cmd"
        dataSource={cfmTable}
        columns={[
          {
            title: '分组', dataIndex: 'category', key: 'category', width: 90,
            filters: [...new Set(cfmTable.map((r) => r.category))].map((c) => ({ text: c, value: c })),
            onFilter: (v, r) => r.category === v,
            render: (v: string) => (
              <Tag color={
                v === '服务管理' ? 'blue' :
                v === '信息查看' ? 'green' :
                v === '安装维护' ? 'orange' :
                v === '进阶辅助' ? 'purple' :
                'default'
              }>{v}</Tag>
            ),
          },
          {
            title: '命令', dataIndex: 'cmd', key: 'cmd', width: 200,
            render: (v: string) => (
              <Space size={4}>
                <Text code style={{ fontSize: 12.5 }}>{v}</Text>
                <Button
                  type="text" size="small" icon={<CopyOutlined />}
                  onClick={() => copyText(v)}
                  style={{ padding: '0 4px', height: 22 }}
                />
              </Space>
            ),
          },
          { title: '作用', dataIndex: 'desc', key: 'desc', ellipsis: false },
        ]}
      />
      <Paragraph type="secondary" style={{ marginTop: 14, marginBottom: 0, fontSize: 12 }}>
        Windows 同样提供 <Text code>cfm</Text>（在 PowerShell 或 cmd 中执行；安装目录已加入系统 PATH，新开终端生效）。原生命令也行：systemd 用 <Text code>systemctl status cfdmgrd</Text>；macOS 用 <Text code>launchctl list | grep cfdmgrd</Text>；Windows 用 <Text code>services.msc</Text>。
      </Paragraph>
      <Divider style={{ margin: '14px 0' }} />
      <Title level={5} style={{ margin: '0 0 8px' }}>忘了 API 令牌？</Title>
      <CodeBlock code="cfm info" token={token} onCopy={copyText} language="sh" />
    </div>
  );
}

// ---- 5. 环境变量 ----
function renderEnvTab(opts: { token: TokenLike }) {
  const { token } = opts;

  const envs: Array<{ key: string; required: string; default: string; desc: string }> = [
    { key: 'CFDM_API_TOKEN',           required: '✓', default: '—',                desc: 'API 鉴权 Bearer 令牌，登录管理面板的凭证。建议 openssl rand -hex 32 生成。' },
    { key: 'CFDM_HTTP_ADDR',           required: '',  default: ':8080',            desc: '监听地址，可只填端口(如 8080，自动补为 :8080)或 :端口/ip:端口。' },
    { key: 'CFDM_DATA_DIR',            required: '',  default: '/var/lib/cfdmgrd', desc: '数据根目录。子目录：profiles/、logs/、stores/、bin/、meta.json、metrics.db。' },
    { key: 'CFDM_CORS_ORIGINS',        required: '',  default: '*',                desc: '逗号分隔的 CORS 白名单。前后端分离调试时填具体 origin。' },
    { key: 'CFDM_LOG_LEVEL',           required: '',  default: 'info',             desc: 'trace / debug / info / warn / error。' },
    { key: 'CFDM_DOCS_ENABLED',        required: '',  default: 'true',             desc: '是否开放 /api/docs Scalar 在线文档（生产可关闭）。' },
    { key: 'CFDM_RELEASE_PROXY_BASES', required: '',  default: '7 个 gh-raw 域名', desc: 'cloudflared 二进制下载走的 Release 代理域名（CSV，自动故障转移）。' },
    { key: 'CFDM_BINARIES_DIR',        required: '',  default: '$DATA_DIR/bin/cloudflared', desc: 'cloudflared 二进制存放目录。' },
    { key: 'CFDM_SELF_UPDATE_ENABLED', required: '',  default: 'true',             desc: '是否允许在「关于」页一键自更新并重启（Docker/手动运行不支持，自动置灰）。' },
  ];

  return (
    <div style={{ padding: 18 }}>
      <Alert
        type="info"
        showIcon
        message="所有环境变量统一以 CFDM_ 前缀。一键安装后写入 /etc/cfdmgrd/cfdmgrd.env（Linux）/ launchd plist（macOS）/ NSSM 服务环境（Windows）；Docker 通过 -e 或 environment: 块传入。"
        style={{ marginBottom: 16 }}
      />
      <Table
        size="small"
        pagination={false}
        rowKey="key"
        dataSource={envs}
        columns={[
          { title: '变量', dataIndex: 'key', key: 'key', width: 230,
            render: (v: string) => <Text code style={{ fontSize: 12.5 }} copyable>{v}</Text> },
          { title: '必填', dataIndex: 'required', key: 'required', width: 60, align: 'center',
            render: (v: string) => v ? <Tag color="red">必填</Tag> : <Tag>可选</Tag> },
          { title: '默认', dataIndex: 'default', key: 'default', width: 130,
            render: (v: string) => <Text style={{ fontFamily: 'monospace', fontSize: 12 }}>{v}</Text> },
          { title: '说明', dataIndex: 'desc', key: 'desc' },
        ]}
      />
      <Divider style={{ margin: '18px 0 12px' }} />
      <Title level={5} style={{ margin: '0 0 8px' }}>配置文件 / 数据目录位置（一键安装后）</Title>
      <Descriptions column={1} size="small" bordered labelStyle={{ width: 130, background: token.colorFillTertiary }}>
        <Descriptions.Item label="Linux">
          配置：<Text code>/etc/cfdmgrd/cfdmgrd.env</Text> ｜ 数据：<Text code>/var/lib/cfdmgrd/</Text>
        </Descriptions.Item>
        <Descriptions.Item label="macOS">
          配置：写在 launchd plist ｜ 数据：<Text code>/usr/local/var/cfdmgrd/</Text>
        </Descriptions.Item>
        <Descriptions.Item label="Windows">
          数据：<Text code>%ProgramData%\cfdmgrd\data\</Text>
        </Descriptions.Item>
        <Descriptions.Item label="Docker">
          通过 <Text code>-e</Text> / <Text code>environment:</Text> 传入；数据挂卷到 <Text code>/data</Text>
        </Descriptions.Item>
      </Descriptions>
    </div>
  );
}
