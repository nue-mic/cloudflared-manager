import { useEffect, useState, useRef } from 'react';
import { Card, Select, Button, Space, Input, Switch, Typography, Empty, App, Badge, theme as antdTheme } from 'antd';
import {
  DeleteOutlined,
  PlayCircleOutlined,
  PauseCircleOutlined,
  ArrowDownOutlined,
} from '@ant-design/icons';
import client, { getAPIToken } from '../api/client';

const { Title, Text } = Typography;
const { Option } = Select;

interface ConfigOption {
  id: string;
  name: string;
  status: string;
}

const Logs: React.FC = () => {
  const { token } = antdTheme.useToken();
  const { message } = App.useApp();
  const [configs, setConfigs] = useState<ConfigOption[]>([]);
  const [selectedId, setSelectedId] = useState<string>('');
  const [logs, setLogs] = useState<string[]>([]);
  const [filterText, setFilterText] = useState<string>('');
  const [autoScroll, setAutoScroll] = useState<boolean>(true);
  const [isPaused, setIsPaused] = useState<boolean>(false);
  const [wsStatus, setWsStatus] = useState<'disconnected' | 'connecting' | 'connected'>('disconnected');

  const wsRef = useRef<WebSocket | null>(null);
  const logContainerRef = useRef<HTMLPreElement | null>(null);
  const isPausedRef = useRef(isPaused);
  isPausedRef.current = isPaused;

  useEffect(() => {
    fetchConfigs();
    return () => {
      disconnectWS();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (autoScroll && logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
    }
  }, [logs, autoScroll]);

  useEffect(() => {
    if (selectedId) {
      connectWS(selectedId);
    } else {
      disconnectWS();
      setLogs([]);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedId]);

  const fetchConfigs = async () => {
    try {
      const resp = await client.get('/api/v1/configs');
      if (resp.status === 200) {
        const items = resp.data?.items || resp.data || [];
        const mapped = await Promise.all(items.map(async (item: any) => {
          let state = 'stopped';
          try {
            const stResp = await client.get(`/api/v1/configs/${item.id}/status`);
            state = stResp.data?.status || stResp.data?.state || 'stopped';
          } catch {
            // ignore
          }
          return {
            id: item.id,
            name: item.name || item.frpsmgr?.name || item.id,
            status: state,
          };
        }));
        setConfigs(mapped);

        const running = mapped.find((c) => c.status === 'started' || c.status === 'running');
        if (running) {
          setSelectedId(running.id);
        } else if (mapped.length > 0) {
          setSelectedId(mapped[0].id);
        }
      }
    } catch {
      message.error('加载实例列表失败');
    }
  };

  const connectWS = (configId: string) => {
    disconnectWS();
    setLogs([]);
    setWsStatus('connecting');

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const host = window.location.host;
    const apiToken = getAPIToken();
    const wsUrl = `${protocol}//${host}/api/v1/configs/${configId}/logs/tail?token=${encodeURIComponent(apiToken || '')}`;

    try {
      const ws = new WebSocket(wsUrl);
      wsRef.current = ws;

      ws.onopen = () => {
        setWsStatus('connected');
      };

      ws.onmessage = (event) => {
        if (isPausedRef.current) return;
        try {
          const data = JSON.parse(event.data);
          if (data && typeof data.line === 'string') {
            setLogs((prev) => {
              const updated = [...prev, data.line];
              if (updated.length > 1000) updated.shift();
              return updated;
            });
          }
        } catch {
          if (typeof event.data === 'string') {
            setLogs((prev) => [...prev, event.data]);
          }
        }
      };

      ws.onerror = () => setWsStatus('disconnected');
      ws.onclose = () => setWsStatus('disconnected');
    } catch {
      setWsStatus('disconnected');
      message.error('WebSocket 连接建立失败');
    }
  };

  const disconnectWS = () => {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    setWsStatus('disconnected');
  };

  const getLogLineClass = (line: string): string => {
    if (line.includes('[W]') || /warn/i.test(line)) return 'log-line log-warn';
    if (line.includes('[E]') || /error/i.test(line)) return 'log-line log-error';
    if (line.includes('[D]') || /debug/i.test(line)) return 'log-line log-debug';
    if (line.includes('[I]') || /info/i.test(line)) return 'log-line log-info';
    return 'log-line';
  };

  const filteredLogs = logs.filter((line) =>
    line.toLowerCase().includes(filterText.toLowerCase())
  );

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={4} style={{ margin: 0 }}>实时日志流监控</Title>

        <Space>
          <Text type="secondary">选择实例:</Text>
          <Select value={selectedId} onChange={setSelectedId} style={{ width: 240 }}>
            {configs.map((c) => (
              <Option key={c.id} value={c.id}>
                <Space>
                  <span style={{
                    width: 6, height: 6, borderRadius: '50%', display: 'inline-block',
                    background: (c.status === 'started' || c.status === 'running') ? '#52c41a' : '#d9d9d9',
                  }} />
                  {c.name}{c.status === 'started' || c.status === 'running' ? ' (运行中)' : ''}
                </Space>
              </Option>
            ))}
          </Select>

          <Badge
            status={wsStatus === 'connected' ? 'success' : wsStatus === 'connecting' ? 'processing' : 'default'}
            text={
              <Text style={{ color: wsStatus === 'connected' ? token.colorSuccess : wsStatus === 'connecting' ? token.colorPrimary : token.colorTextSecondary }}>
                {wsStatus === 'connected' ? 'WS 已连接' : wsStatus === 'connecting' ? '连接中…' : '断开'}
              </Text>
            }
          />
        </Space>
      </div>

      <Card
        styles={{ body: { padding: 16, height: '100%', display: 'flex', flexDirection: 'column' } }}
        style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden', minHeight: 450, borderRadius: 10 }}
      >
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
          <Space wrap>
            <Input
              placeholder="过滤日志关键字…"
              value={filterText}
              onChange={(e) => setFilterText(e.target.value)}
              style={{ width: 220 }}
              allowClear
            />
            <Button
              icon={isPaused ? <PlayCircleOutlined /> : <PauseCircleOutlined />}
              onClick={() => setIsPaused(!isPaused)}
            >
              {isPaused ? '继续接收' : '暂停接收'}
            </Button>
            <Button danger icon={<DeleteOutlined />} onClick={() => setLogs([])}>
              清空面板
            </Button>
          </Space>

          <Space>
            <Text type="secondary" style={{ fontSize: 13 }}>自动滚动底部:</Text>
            <Switch checked={autoScroll} onChange={setAutoScroll} size="small" />
            <Button
              size="small"
              icon={<ArrowDownOutlined />}
              onClick={() => {
                if (logContainerRef.current) {
                  logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
                }
              }}
            />
          </Space>
        </div>

        <div style={{ flex: 1, position: 'relative', overflow: 'hidden', borderRadius: 8 }}>
          {selectedId ? (
            <pre
              ref={logContainerRef}
              className="terminal-container"
              style={{ position: 'absolute', inset: 0, margin: 0, overflowY: 'auto' }}
            >
              {filteredLogs.length > 0 ? (
                filteredLogs.map((line, index) => (
                  <div key={index} className={getLogLineClass(line)}>
                    {line}
                  </div>
                ))
              ) : (
                <div style={{ padding: '40px 0', textAlign: 'center', opacity: 0.55 }}>
                  {filterText ? '未搜索到匹配的日志行' : '暂无日志输出，等待 WebSocket 推送…'}
                </div>
              )}
            </pre>
          ) : (
            <div style={{ height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <Empty description="请先选择一个 cloudflared 实例" />
            </div>
          )}
        </div>
      </Card>
    </div>
  );
};

export default Logs;
