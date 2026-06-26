import { useState, useEffect } from 'react';
import { Input, Button, Form, Typography, App, theme as antdTheme, Grid, Tag } from 'antd';
import {
  KeyOutlined,
  ArrowRightOutlined,
  ClusterOutlined,
  MonitorOutlined,
  LineChartOutlined,
  BellOutlined,
  GithubOutlined,
  BookOutlined,
  ThunderboltFilled,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import client, { setAPIToken, getAPIToken } from '../api/client';
import BrandMark from '../components/BrandMark';
import { useBranding } from '../branding/BrandingContext';

const { Title, Text, Link: ATypoLink } = Typography;

const Login: React.FC = () => {
  const navigate = useNavigate();
  const { message } = App.useApp();
  const { token } = antdTheme.useToken();
  const screens = Grid.useBreakpoint();
  const { branding } = useBranding();
  const [loading, setLoading] = useState<boolean>(false);

  useEffect(() => {
    if (getAPIToken()) {
      navigate('/dashboard');
    }
  }, [navigate]);

  const onFinish = async (values: { token: string }) => {
    setLoading(true);
    try {
      setAPIToken(values.token);
      const resp = await client.get('/api/v1/version');
      if (resp.status === 200) {
        message.success('连接成功，已授权登录');
        navigate('/dashboard');
      } else {
        throw new Error('鉴权未通过');
      }
    } catch {
      setAPIToken('');
      message.error('Token 校验失败，请确认守护进程是否已配置该密钥');
    } finally {
      setLoading(false);
    }
  };

  // 大屏分左右；小屏堆叠
  const isWide = !!screens.md;

  const features = [
    { icon: <ClusterOutlined />, label: '多实例并行' },
    { icon: <MonitorOutlined />, label: '实时监控' },
    { icon: <LineChartOutlined />, label: '历史曲线' },
    { icon: <BellOutlined />, label: '智能告警' },
  ];

  return (
    <>
      {/* 动画 keyframes 与浮动光球样式（注入到全局） */}
      <style>{`
        @keyframes login-blob-a {
          0%, 100% { transform: translate(0, 0) scale(1); }
          33% { transform: translate(40px, -30px) scale(1.08); }
          66% { transform: translate(-30px, 20px) scale(0.95); }
        }
        @keyframes login-blob-b {
          0%, 100% { transform: translate(0, 0) scale(1); }
          50% { transform: translate(-50px, 40px) scale(1.1); }
        }
        @keyframes login-grid-pan {
          0% { background-position: 0 0; }
          100% { background-position: 60px 60px; }
        }
        @keyframes login-fade-up {
          0% { opacity: 0; transform: translateY(12px); }
          100% { opacity: 1; transform: translateY(0); }
        }
        .login-fade-up { animation: login-fade-up 0.6s ease-out both; }
        .login-fade-up-1 { animation-delay: 0.05s; }
        .login-fade-up-2 { animation-delay: 0.15s; }
        .login-fade-up-3 { animation-delay: 0.25s; }
        .login-fade-up-4 { animation-delay: 0.35s; }
        .login-feature-chip {
          display: inline-flex; align-items: center; gap: 6px;
          padding: 6px 12px;
          background: rgba(255,255,255,0.06);
          border: 1px solid rgba(255,255,255,0.12);
          border-radius: 999px;
          color: rgba(255,255,255,0.85);
          font-size: 12.5px;
          backdrop-filter: blur(8px);
          transition: all 0.2s ease;
        }
        .login-feature-chip:hover {
          background: rgba(255,255,255,0.12);
          border-color: rgba(255,255,255,0.2);
          transform: translateY(-1px);
        }
        .login-submit-btn {
          height: 48px !important;
          font-weight: 600 !important;
          letter-spacing: 0.5px;
          background: linear-gradient(135deg, #6366f1 0%, #8b5cf6 50%, #ec4899 100%) !important;
          border: none !important;
          box-shadow: 0 8px 24px rgba(99, 102, 241, 0.35), 0 2px 6px rgba(236, 72, 153, 0.25) !important;
          transition: transform 0.2s ease, box-shadow 0.2s ease !important;
        }
        .login-submit-btn:hover {
          transform: translateY(-1px);
          box-shadow: 0 12px 30px rgba(99, 102, 241, 0.45), 0 4px 10px rgba(236, 72, 153, 0.3) !important;
        }
        .login-input-wrap .ant-input-affix-wrapper {
          height: 48px;
          border-radius: 12px;
          background: rgba(0, 0, 0, 0.02);
          border: 1.5px solid ${token.colorBorder};
          transition: all 0.2s ease;
        }
        .login-input-wrap .ant-input-affix-wrapper:hover,
        .login-input-wrap .ant-input-affix-wrapper-focused {
          border-color: #8b5cf6;
          box-shadow: 0 0 0 3px rgba(139, 92, 246, 0.1);
        }
      `}</style>

      <div
        style={{
          minHeight: '100vh',
          width: '100vw',
          display: 'flex',
          flexDirection: isWide ? 'row' : 'column',
          background: '#0b1020',
          overflow: 'hidden',
          position: 'relative',
        }}
      >
        {/* 全局深色底 + 网格图案 + 浮动光球 */}
        <div
          aria-hidden
          style={{
            position: 'absolute',
            inset: 0,
            backgroundImage:
              'linear-gradient(rgba(99,102,241,0.06) 1px, transparent 1px), linear-gradient(90deg, rgba(99,102,241,0.06) 1px, transparent 1px)',
            backgroundSize: '60px 60px',
            animation: 'login-grid-pan 30s linear infinite',
            maskImage: 'radial-gradient(ellipse at center, black 30%, transparent 80%)',
            WebkitMaskImage: 'radial-gradient(ellipse at center, black 30%, transparent 80%)',
            pointerEvents: 'none',
          }}
        />
        {/* 浮动光球 A — 紫 */}
        <div
          aria-hidden
          style={{
            position: 'absolute',
            top: '-12%',
            left: '-8%',
            width: 520,
            height: 520,
            borderRadius: '50%',
            background:
              'radial-gradient(circle, rgba(139,92,246,0.45) 0%, rgba(139,92,246,0) 70%)',
            filter: 'blur(20px)',
            animation: 'login-blob-a 18s ease-in-out infinite',
            pointerEvents: 'none',
          }}
        />
        {/* 浮动光球 B — 青蓝 */}
        <div
          aria-hidden
          style={{
            position: 'absolute',
            bottom: '-15%',
            left: '20%',
            width: 480,
            height: 480,
            borderRadius: '50%',
            background:
              'radial-gradient(circle, rgba(6,182,212,0.35) 0%, rgba(6,182,212,0) 70%)',
            filter: 'blur(20px)',
            animation: 'login-blob-b 22s ease-in-out infinite',
            pointerEvents: 'none',
          }}
        />
        {/* 浮动光球 C — 粉 */}
        <div
          aria-hidden
          style={{
            position: 'absolute',
            top: '20%',
            right: '-5%',
            width: 460,
            height: 460,
            borderRadius: '50%',
            background:
              'radial-gradient(circle, rgba(236,72,153,0.30) 0%, rgba(236,72,153,0) 70%)',
            filter: 'blur(20px)',
            animation: 'login-blob-a 20s ease-in-out infinite reverse',
            pointerEvents: 'none',
          }}
        />

        {/* 左侧：品牌 Hero */}
        <div
          style={{
            flex: isWide ? '1 1 55%' : '0 0 auto',
            display: 'flex',
            flexDirection: 'column',
            justifyContent: isWide ? 'space-between' : 'flex-start',
            padding: isWide ? '64px 56px' : '40px 24px 20px',
            color: '#fff',
            position: 'relative',
            zIndex: 1,
            minHeight: isWide ? 'auto' : 'unset',
          }}
        >
          <div className="login-fade-up login-fade-up-1" style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <div
              style={{
                width: 40,
                height: 40,
                borderRadius: 10,
                background:
                  'linear-gradient(135deg, #6366f1 0%, #8b5cf6 50%, #ec4899 100%)',
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                boxShadow: '0 6px 18px rgba(139,92,246,0.45)',
              }}
            >
              <BrandMark size={24} color="#fff" />
            </div>
            <Text strong style={{ color: '#fff', fontSize: 17, letterSpacing: 0.5 }}>
              {branding.app_name}
            </Text>
          </div>

          <div style={{ marginTop: isWide ? 0 : 28 }}>
            <div className="login-fade-up login-fade-up-2">
              <Title
                level={1}
                style={{
                  color: '#fff',
                  fontWeight: 800,
                  fontSize: isWide ? 48 : 32,
                  lineHeight: 1.15,
                  marginBottom: 16,
                  letterSpacing: '-0.5px',
                }}
              >
                掌控你的 cloudflared 隧道
                <br />
                <span
                  style={{
                    background:
                      'linear-gradient(135deg, #a5b4fc 0%, #c4b5fd 50%, #f9a8d4 100%)',
                    WebkitBackgroundClip: 'text',
                    WebkitTextFillColor: 'transparent',
                    backgroundClip: 'text',
                  }}
                >
                  从未如此简单
                </span>
              </Title>
            </div>

            <div className="login-fade-up login-fade-up-3">
              <Text
                style={{
                  color: 'rgba(255,255,255,0.7)',
                  fontSize: isWide ? 16 : 14,
                  lineHeight: 1.7,
                  maxWidth: 540,
                  display: 'block',
                }}
              >
                多实例并行托管、实时日志 / 状态监控、历史指标、可视化 token + 参数编辑、阈值告警、
                cloudflared 二进制版本管理 — 一个守护进程把所有 cloudflared 隧道的运维都管起来。
              </Text>
            </div>

            <div
              className="login-fade-up login-fade-up-4"
              style={{ marginTop: 28, display: 'flex', flexWrap: 'wrap', gap: 10 }}
            >
              {features.map((f) => (
                <span key={f.label} className="login-feature-chip">
                  {f.icon}
                  {f.label}
                </span>
              ))}
            </div>
          </div>

          {isWide && (
            <div
              className="login-fade-up login-fade-up-4"
              style={{
                display: 'flex',
                gap: 16,
                color: 'rgba(255,255,255,0.45)',
                fontSize: 12.5,
              }}
            >
              <ATypoLink
                href="https://github.com/nue-mic/cloudflared-manager"
                target="_blank"
                style={{ color: 'rgba(255,255,255,0.6)' }}
              >
                <GithubOutlined /> GitHub
              </ATypoLink>
              <ATypoLink
                href="/api/docs/"
                target="_blank"
                style={{ color: 'rgba(255,255,255,0.6)' }}
              >
                <BookOutlined /> API 文档
              </ATypoLink>
              <span>
                <ThunderboltFilled style={{ color: '#fbbf24' }} /> v1 OpenAPI
              </span>
            </div>
          )}
        </div>

        {/* 右侧：登录表单卡片（毛玻璃） */}
        <div
          style={{
            flex: isWide ? '1 1 45%' : '1 1 auto',
            display: 'flex',
            justifyContent: 'center',
            alignItems: 'center',
            padding: isWide ? '40px 56px' : '20px 24px 40px',
            position: 'relative',
            zIndex: 1,
          }}
        >
          <div
            className="login-fade-up login-fade-up-2"
            style={{
              width: '100%',
              maxWidth: 420,
              padding: '36px 32px',
              borderRadius: 24,
              background:
                token.colorBgContainer === '#ffffff'
                  ? 'rgba(255,255,255,0.85)'
                  : 'rgba(20, 24, 48, 0.72)',
              backdropFilter: 'blur(24px) saturate(180%)',
              WebkitBackdropFilter: 'blur(24px) saturate(180%)',
              border: '1px solid rgba(255,255,255,0.18)',
              boxShadow:
                '0 24px 60px rgba(0,0,0,0.45), 0 0 0 1px rgba(255,255,255,0.05) inset',
            }}
          >
            {/* logo + 标题 */}
            <div style={{ textAlign: 'center', marginBottom: 28 }}>
              <div
                style={{
                  width: 56,
                  height: 56,
                  borderRadius: 16,
                  background:
                    'linear-gradient(135deg, rgba(99,102,241,0.2), rgba(236,72,153,0.18))',
                  border: '1px solid rgba(139,92,246,0.35)',
                  display: 'inline-flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  marginBottom: 16,
                  boxShadow: '0 8px 20px rgba(139,92,246,0.25)',
                }}
              >
                <BrandMark size={30} color="#F6821F" />
              </div>
              <Title
                level={3}
                style={{ margin: 0, fontWeight: 700, fontSize: 22, color: token.colorText }}
              >
                欢迎回来
              </Title>
              <Text
                style={{
                  fontSize: 13.5,
                  color: token.colorTextSecondary,
                  display: 'block',
                  marginTop: 6,
                }}
              >
                请输入 <code style={{ fontSize: 12.5 }}>cfdmgrd</code> 守护进程的 API 令牌
              </Text>
            </div>

            <Form
              name="login"
              onFinish={onFinish}
              layout="vertical"
              requiredMark={false}
              className="login-input-wrap"
            >
              <Form.Item
                name="token"
                rules={[{ required: true, message: '请输入 API 令牌密钥！' }]}
                style={{ marginBottom: 18 }}
              >
                <Input.Password
                  prefix={
                    <KeyOutlined style={{ color: token.colorTextTertiary, fontSize: 16 }} />
                  }
                  placeholder="API Token (Bearer 令牌)"
                  size="large"
                  autoFocus
                />
              </Form.Item>

              <Form.Item style={{ marginBottom: 0 }}>
                <Button
                  type="primary"
                  htmlType="submit"
                  size="large"
                  loading={loading}
                  block
                  className="login-submit-btn"
                  icon={<ArrowRightOutlined />}
                >
                  验证并进入控制台
                </Button>
              </Form.Item>
            </Form>

            <div
              style={{
                marginTop: 20,
                paddingTop: 16,
                borderTop: `1px dashed ${token.colorBorderSecondary}`,
                textAlign: 'center',
              }}
            >
              <Text style={{ fontSize: 12, color: token.colorTextTertiary }}>
                忘了令牌？请在服务器执行{' '}
                <Tag style={{ marginInlineEnd: 0, fontSize: 11.5 }}>cfm info</Tag>
              </Text>
            </div>
          </div>
        </div>
      </div>
    </>
  );
};

export default Login;
