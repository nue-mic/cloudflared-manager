import React, { useEffect, useState, useMemo } from 'react';
import { Layout, Menu, Button, Space, Typography, Modal, Tag, Tooltip, Badge, theme as antdTheme, App } from 'antd';
import {
  DashboardOutlined,
  ClusterOutlined,
  FileTextOutlined,
  HddOutlined,
  ToolOutlined,
  SettingOutlined,
  SwapOutlined,
  PoweroffOutlined,
  SafetyCertificateOutlined,
  LineChartOutlined,
  BellOutlined,
  InfoCircleOutlined,
  CloudDownloadOutlined,
} from '@ant-design/icons';
import type { MenuProps } from 'antd';
import { useNavigate, useLocation, Outlet } from 'react-router-dom';
import client, { getAPIToken, clearAPIToken } from '../api/client';
import { checkVersion } from '../api/update';
import ThemeSwitcher from '../theme/ThemeSwitcher';
import { useEventStream } from '../events/EventStreamContext';

const { Header, Sider, Content } = Layout;
const { Text } = Typography;

type MenuItem = Required<MenuProps>['items'][number];

const MainLayout: React.FC = () => {
  const navigate = useNavigate();
  const location = useLocation();
  const { message } = App.useApp();
  const { token } = antdTheme.useToken();
  const stream = useEventStream();

  const [version, setVersion] = useState<string>('获取中…');
  const [hasUpdate, setHasUpdate] = useState(false);
  const [latestVer, setLatestVer] = useState<string>('');

  useEffect(() => {
    const t = getAPIToken();
    if (!t) {
      navigate('/login');
    } else {
      fetchSystemVersion();
      fetchUpdateState();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navigate]);

  const fetchSystemVersion = async () => {
    try {
      const resp = await client.get('/api/v1/version');
      if (resp.status === 200) {
        setVersion(resp.data.daemon || resp.data.version || '');
      }
    } catch {
      // 静默：实时连接状态由 WebSocket 反映
    }
  };

  // 后台静默检查是否有新版本；命中后端 ~1h 缓存，开销很小。
  const fetchUpdateState = async () => {
    try {
      const r = await checkVersion(false);
      setHasUpdate(!!r.has_update);
      setLatestVer(r.latest || '');
    } catch {
      // 静默失败：检查更新非关键路径
    }
  };

  const handleLogout = () => {
    Modal.confirm({
      title: '确认注销登录？',
      content: '退出后将清除本地 API 令牌，需重新输入才能继续使用。',
      okText: '退出',
      cancelText: '取消',
      okButtonProps: { danger: true },
      onOk: () => {
        clearAPIToken();
        message.success('已安全登出');
        navigate('/login');
      },
    });
  };

  const menuItems: MenuItem[] = useMemo(
    () => [
      {
        key: 'g-overview',
        type: 'group',
        label: '总览',
        children: [
          { key: '/dashboard', icon: <DashboardOutlined />, label: '仪表盘' },
        ],
      },
      {
        key: 'g-runtime',
        type: 'group',
        label: '运行',
        children: [
          { key: '/configs', icon: <ClusterOutlined />, label: 'cloudflared 实例' },
          { key: '/binaries', icon: <CloudDownloadOutlined />, label: '二进制管理' },
          { key: '/traffic', icon: <LineChartOutlined />, label: '历史流量' },
          { key: '/alerts', icon: <BellOutlined />, label: '告警' },
          { key: '/logs', icon: <FileTextOutlined />, label: '日志流' },
        ],
      },
      {
        key: 'g-host',
        type: 'group',
        label: '主机',
        children: [{ key: '/system', icon: <HddOutlined />, label: '系统监控' }],
      },
      {
        key: 'g-tools',
        type: 'group',
        label: '工具',
        children: [
          { key: '/tools/validate', icon: <ToolOutlined />, label: '配置校验' },
          { key: '/import-export', icon: <SwapOutlined />, label: '导入 / 导出' },
        ],
      },
      {
        key: 'g-system',
        type: 'group',
        label: '系统',
        children: [{ key: '/settings', icon: <SettingOutlined />, label: '设置' }],
      },
      {
        key: 'g-about',
        type: 'group',
        label: '帮助',
        children: [{ key: '/about', icon: <InfoCircleOutlined />, label: '关于 & 手册' }],
      },
    ],
    []
  );

  // 根据 path 选中：取首段或两段做匹配
  const selectedKey = useMemo(() => {
    const p = location.pathname;
    const candidates = ['/tools/validate', '/import-export', '/binaries'];
    for (const c of candidates) if (p.startsWith(c)) return c;
    const seg = '/' + p.split('/').filter(Boolean)[0];
    return seg || '/dashboard';
  }, [location.pathname]);

  const connState = stream.state;
  const connTone: Record<typeof connState, { dot: string; text: string; label: string }> = {
    idle: { dot: token.colorTextDisabled, text: token.colorTextSecondary, label: '未连接' },
    connecting: { dot: token.colorWarning, text: token.colorWarning, label: '连接中…' },
    open: { dot: token.colorSuccess, text: token.colorSuccess, label: '事件流已就绪' },
    closed: { dot: token.colorError, text: token.colorError, label: '已断开' },
    error: { dot: token.colorError, text: token.colorError, label: '连接异常' },
  };
  const tone = connTone[connState];

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider
        width={232}
        theme="dark"
        breakpoint="lg"
        collapsedWidth={64}
        style={{ position: 'sticky', top: 0, height: '100vh' }}
      >
        <div
          style={{
            height: 56,
            display: 'flex',
            alignItems: 'center',
            gap: 10,
            padding: '0 16px',
            borderBottom: '1px solid rgba(255,255,255,0.06)',
          }}
        >
          <SafetyCertificateOutlined style={{ fontSize: 22, color: token.colorPrimary }} />
          <Text strong style={{ color: '#fff', fontSize: 15, letterSpacing: 0.5 }}>
            Cloudflared Manager
          </Text>
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[selectedKey]}
          onClick={({ key }) => navigate(key)}
          items={menuItems}
          style={{ borderInlineEnd: 'none', marginTop: 8 }}
        />
      </Sider>

      <Layout>
        <Header
          style={{
            background: token.colorBgContainer,
            borderBottom: `1px solid ${token.colorBorderSecondary}`,
            padding: '0 20px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            height: 56,
            position: 'sticky',
            top: 0,
            zIndex: 5,
          }}
        >
          <Space size="middle" align="center">
            <Tooltip title={tone.label}>
              <Space size={8} align="center">
                <span
                  aria-label={tone.label}
                  style={{
                    width: 8,
                    height: 8,
                    borderRadius: '50%',
                    background: tone.dot,
                    boxShadow: `0 0 0 3px ${tone.dot}22`,
                    display: 'inline-block',
                  }}
                />
                <Text style={{ color: tone.text, fontSize: 13 }}>{tone.label}</Text>
              </Space>
            </Tooltip>
            {stream.lastSeq > 0 && (
              <Tag bordered={false} color="processing" style={{ marginInlineStart: 4 }}>
                seq #{stream.lastSeq}
              </Tag>
            )}
          </Space>

          <Space size="middle" align="center">
            <Tooltip title={hasUpdate ? `发现新版本 ${latestVer}，点击前往升级` : '后端版本'}>
              <Badge dot={hasUpdate} offset={[-2, 4]}>
                <Tag
                  bordered={false}
                  onClick={() => navigate('/about')}
                  style={{ cursor: 'pointer' }}
                >
                  cfdmgrd v{version || '—'}
                </Tag>
              </Badge>
            </Tooltip>
            <ThemeSwitcher />
            <Button type="text" danger icon={<PoweroffOutlined />} onClick={handleLogout}>
              登出
            </Button>
          </Space>
        </Header>

        <Content
          style={{
            margin: 20,
            padding: 0,
            background: 'transparent',
            minHeight: 'calc(100vh - 96px)',
          }}
        >
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  );
};

export default MainLayout;
