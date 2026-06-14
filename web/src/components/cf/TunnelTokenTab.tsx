// 隧道「Token」Tab：直接展示 connector token（明文 + 一键复制），并按各操作系统
// 列出 cloudflared 连接器的安装 / 运行命令（命令内已嵌入本隧道 token，逐条可复制）。
//
// 同一隧道的 connector token 全平台通用，故顶部「主 token」单独一键复制；下面各系统
// 命令只是把这同一个 token 拼进去。本项目自用，不再对 token 做遮罩。

import { useCallback, useEffect, useState } from 'react';
import { Card, Button, Space, Typography, Alert, App, Skeleton, Tag } from 'antd';
import { KeyOutlined, CopyOutlined, ReloadOutlined } from '@ant-design/icons';
import { cfApi } from '../../api/client';

const { Text } = Typography;

interface Props {
  aid: string;
  tid: string;
}

interface CmdItem {
  key: string;
  label: string;
  note?: string;
  cmd: string;
}

// 各系统 cloudflared 连接器安装 / 运行命令（token 通用，直接嵌入）。
function buildCommands(token: string): CmdItem[] {
  const t = token || '<TOKEN>';
  return [
    {
      key: 'run',
      label: '前台运行（任意平台 · 调试用）',
      note: '直接拉起连接，Ctrl+C 退出；适合先验证 token 是否可用。',
      cmd: `cloudflared tunnel run --token ${t}`,
    },
    {
      key: 'linux',
      label: 'Linux · 安装为系统服务（已装 cloudflared）',
      note: '装成 systemd 服务并开机自启。',
      cmd: `sudo cloudflared service install ${t}`,
    },
    {
      key: 'debian',
      label: 'Debian / Ubuntu（apt 安装 + 服务）',
      cmd: `curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared $(lsb_release -cs) main" | sudo tee /etc/apt/sources.list.d/cloudflared.list
sudo apt-get update && sudo apt-get install -y cloudflared
sudo cloudflared service install ${t}`,
    },
    {
      key: 'rhel',
      label: 'RHEL / CentOS / Rocky / Fedora（yum / dnf）',
      cmd: `curl -fsSL https://pkg.cloudflare.com/cloudflared-ascii.repo | sudo tee /etc/yum.repos.d/cloudflared.repo
sudo yum install -y cloudflared
sudo cloudflared service install ${t}`,
    },
    {
      key: 'windows',
      label: 'Windows（.msi + 服务）',
      note: '先到 GitHub Releases 下载 cloudflared-windows-amd64.msi 安装，再在管理员 PowerShell 执行：',
      cmd: `cloudflared.exe service install ${t}`,
    },
    {
      key: 'macos',
      label: 'macOS（Homebrew）',
      cmd: `brew install cloudflared
sudo cloudflared service install ${t}`,
    },
    {
      key: 'docker',
      label: 'Docker',
      note: '容器常驻；--no-autoupdate 避免容器内 cloudflared 自更新。',
      cmd: `docker run -d --name cloudflared --restart unless-stopped \\
  cloudflare/cloudflared:latest tunnel --no-autoupdate run --token ${t}`,
    },
  ];
}

const codeBoxStyle: React.CSSProperties = {
  margin: 0,
  padding: '10px 12px',
  borderRadius: 8,
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
  fontSize: 12,
  lineHeight: 1.6,
  background: '#0b1020',
  color: '#d1d5db',
  border: '1px solid rgba(255,255,255,.06)',
  overflowX: 'auto',
};

export default function TunnelTokenTab({ aid, tid }: Props) {
  const { message } = App.useApp();
  const [token, setToken] = useState<string>('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');

  const errMsg = (err: unknown): string => {
    const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
    return e.response?.data?.error?.message || e.message || '未知错误';
  };

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const resp = await cfApi.tunnelToken(aid, tid);
      setToken(resp.data?.token || '');
    } catch (err: unknown) {
      setError('获取 token 失败：' + errMsg(err));
    } finally {
      setLoading(false);
    }
  }, [aid, tid]);

  useEffect(() => {
    load();
  }, [load]);

  const copy = useCallback(
    (text: string, label: string) => {
      const ok = () => message.success(`${label} 已复制`);
      const fail = () => message.error('复制失败，请手动选择文本复制');
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(text).then(ok).catch(fail);
        return;
      }
      try {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.focus();
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
        ok();
      } catch {
        fail();
      }
    },
    [message],
  );

  const commands = buildCommands(token);

  return (
    <Space direction="vertical" size={14} style={{ width: '100%' }}>
      {/* 主 token：明文 + 一键复制 */}
      <Card
        size="small"
        style={{ borderRadius: 10 }}
        title={
          <Space>
            <KeyOutlined /> 隧道 Token（连接器凭证）
          </Space>
        }
        extra={<Button size="small" icon={<ReloadOutlined />} loading={loading} onClick={load}>刷新</Button>}
      >
        {loading ? (
          <Skeleton active paragraph={{ rows: 2 }} />
        ) : error ? (
          <Alert type="error" showIcon message={error} />
        ) : (
          <Space direction="vertical" size={10} style={{ width: '100%' }}>
            <Space size={8} wrap>
              <Button type="primary" icon={<CopyOutlined />} onClick={() => copy(token, '主 token')}>
                一键复制主 token
              </Button>
              <Text type="secondary" style={{ fontSize: 12 }}>
                全平台通用——下面各系统命令都用这同一个 token。
              </Text>
            </Space>
            <pre style={codeBoxStyle}>{token || '（空）'}</pre>
            <Text type="secondary" style={{ fontSize: 12 }}>
              提示：该 token 可直接用于运行 cloudflared 连接此隧道，等同于隧道控制权，注意保管。
            </Text>
          </Space>
        )}
      </Card>

      {/* 各系统连接器安装 / 运行命令 */}
      <Card
        size="small"
        style={{ borderRadius: 10 }}
        title="各系统连接器命令（命令已含本隧道 token，逐条可复制）"
      >
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          {commands.map((c) => (
            <div key={c.key}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, marginBottom: 6 }}>
                <Space size={8} wrap>
                  <Tag color="blue" style={{ marginInlineEnd: 0 }}>{c.label}</Tag>
                  {c.note && <Text type="secondary" style={{ fontSize: 12 }}>{c.note}</Text>}
                </Space>
                <Button size="small" icon={<CopyOutlined />} onClick={() => copy(c.cmd, c.label)} disabled={loading || !!error}>
                  复制
                </Button>
              </div>
              <pre style={codeBoxStyle}>{c.cmd}</pre>
            </div>
          ))}
        </Space>
      </Card>
    </Space>
  );
}
