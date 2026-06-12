import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Select, Input, Button, Space, Switch, Badge, Tooltip, Typography } from 'antd';
import {
  PlayCircleOutlined,
  PauseCircleOutlined,
  DeleteOutlined,
  DownloadOutlined,
  VerticalAlignBottomOutlined,
} from '@ant-design/icons';
import { logStreamUrl } from '../../api/client';
import type { LogEntry, LogStreamFrame } from '../../api/types';
import { fmtTime } from '../../utils/time';

const { Text } = Typography;

// 前端缓冲封顶。封顶后直接整段渲染（无虚拟滚动，KISS）；未来真卡再换虚拟列表。
const MAX_BUFFER = 5000;

const LEVEL_OPTIONS = [
  { value: '', label: '全部级别' },
  { value: 'debug', label: 'DEBUG+' },
  { value: 'info', label: 'INFO+' },
  { value: 'warn', label: 'WARN+' },
  { value: 'error', label: 'ERROR+' },
];

type WsState = 'connecting' | 'open' | 'closed';

interface Props {
  id: string;
  /** 滚动区高度（数字按 px）。默认 460。 */
  height?: number | string;
}

function levelClass(level: string): string {
  switch (level) {
    case 'warn':
      return 'log-warn';
    case 'error':
      return 'log-error';
    case 'fatal':
      return 'log-fatal';
    case 'debug':
      return 'log-debug';
    case 'info':
      return 'log-info';
    default:
      return 'log-unknown';
  }
}

// 在 text 中把（大小写不敏感的）keyword 命中包成高亮 span。
function highlight(text: string, kw: string): ReactNode {
  if (!kw) return text;
  const lower = text.toLowerCase();
  const k = kw.toLowerCase();
  const out: ReactNode[] = [];
  let i = 0;
  let idx = lower.indexOf(k, i);
  let n = 0;
  while (idx >= 0) {
    if (idx > i) out.push(text.slice(i, idx));
    out.push(
      <span className="log-hl" key={`h${n++}`}>
        {text.slice(idx, idx + k.length)}
      </span>
    );
    i = idx + k.length;
    idx = lower.indexOf(k, i);
  }
  if (i < text.length) out.push(text.slice(i));
  return out;
}

/**
 * InstanceLogPanel —— 单实例结构化实时日志面板。
 *
 * 消费 WS /configs/{id}/logs/stream（帧 {entries:[LogEntry]}）：级别着色、
 * 关键字过滤+高亮、跟随底部 + 上滚自动暂停 + 新数据浮标、暂停/清屏/下载/换行/
 * 时间戳开关。级别过滤走服务端（重订阅），关键字过滤走客户端（瞬时）。
 *
 * 同时被 Configs 详情面板与独立 Logs 页复用。
 */
export default function InstanceLogPanel({ id, height = 460 }: Props) {
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [level, setLevel] = useState('');
  const [keyword, setKeyword] = useState('');
  const [paused, setPaused] = useState(false);
  const [follow, setFollow] = useState(true);
  const [wrap, setWrap] = useState(true);
  const [showTs, setShowTs] = useState(true);
  const [newCount, setNewCount] = useState(0);
  const [ws, setWs] = useState<WsState>('connecting');

  const wsRef = useRef<WebSocket | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const followRef = useRef(follow);
  followRef.current = follow;
  const pausedRef = useRef(paused);
  pausedRef.current = paused;
  const lastSeqRef = useRef(0);

  // 连接 WS：id 或服务端级别过滤变化时重订阅。
  useEffect(() => {
    setEntries([]);
    lastSeqRef.current = 0;
    setNewCount(0);
    let closedByUs = false;
    let retry: number | undefined;

    const connect = () => {
      setWs('connecting');
      const url = logStreamUrl(id, { level: level || undefined, backlog: 500 });
      let sock: WebSocket;
      try {
        sock = new WebSocket(url);
      } catch {
        setWs('closed');
        return;
      }
      wsRef.current = sock;
      sock.onopen = () => setWs('open');
      sock.onmessage = (ev) => {
        if (pausedRef.current) return;
        let frame: LogStreamFrame;
        try {
          frame = JSON.parse(ev.data as string);
        } catch {
          return;
        }
        if (!frame?.entries?.length) return;
        setEntries((prev) => {
          const merged = prev.slice();
          let added = 0;
          for (const e of frame.entries) {
            // 服务端订阅先于快照，边界可能重复 → 按 seq 去重。
            if (e.seq <= lastSeqRef.current) continue;
            lastSeqRef.current = e.seq;
            merged.push(e);
            added++;
          }
          if (added === 0) return prev;
          if (merged.length > MAX_BUFFER) merged.splice(0, merged.length - MAX_BUFFER);
          if (!followRef.current) setNewCount((c) => c + added);
          return merged;
        });
      };
      sock.onerror = () => {};
      sock.onclose = () => {
        setWs('closed');
        if (!closedByUs) retry = window.setTimeout(connect, 1500);
      };
    };

    connect();
    return () => {
      closedByUs = true;
      if (retry) clearTimeout(retry);
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, level]);

  // 跟随底部时，新行到达自动滚到底。
  useEffect(() => {
    if (follow && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [entries, follow]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
    if (atBottom) {
      if (!followRef.current) {
        setFollow(true);
        setNewCount(0);
      }
    } else if (followRef.current) {
      setFollow(false);
    }
  };

  const jumpToBottom = () => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
    setFollow(true);
    setNewCount(0);
  };

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase();
    if (!kw) return entries;
    return entries.filter((e) => (e.message + ' ' + e.raw).toLowerCase().includes(kw));
  }, [entries, keyword]);

  const kw = keyword.trim();

  const download = () => {
    const text = filtered.map((e) => e.raw || `${e.time} ${e.level} ${e.message}`).join('\n');
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `${id}-logs.txt`;
    a.click();
    URL.revokeObjectURL(a.href);
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center', marginBottom: 10, justifyContent: 'space-between' }}>
        <Space wrap>
          <Select size="small" style={{ width: 130 }} value={level} onChange={setLevel} options={LEVEL_OPTIONS} />
          <Input
            size="small"
            placeholder="关键字过滤 / 高亮…"
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            style={{ width: 200 }}
            allowClear
          />
          <Button
            size="small"
            icon={paused ? <PlayCircleOutlined /> : <PauseCircleOutlined />}
            onClick={() => setPaused((p) => !p)}
          >
            {paused ? '继续' : '暂停'}
          </Button>
          <Button size="small" danger icon={<DeleteOutlined />} onClick={() => { setEntries([]); setNewCount(0); }}>
            清屏
          </Button>
          <Tooltip title="下载当前日志为 .txt">
            <Button size="small" icon={<DownloadOutlined />} onClick={download} />
          </Tooltip>
        </Space>
        <Space size={12} wrap>
          <Space size={4}>
            <Text type="secondary" style={{ fontSize: 12 }}>时间</Text>
            <Switch size="small" checked={showTs} onChange={setShowTs} />
          </Space>
          <Space size={4}>
            <Text type="secondary" style={{ fontSize: 12 }}>换行</Text>
            <Switch size="small" checked={wrap} onChange={setWrap} />
          </Space>
          <Badge
            status={ws === 'open' ? 'success' : ws === 'connecting' ? 'processing' : 'error'}
            text={<Text style={{ fontSize: 12 }} type="secondary">{ws === 'open' ? '已连接' : ws === 'connecting' ? '连接中' : '断开'}</Text>}
          />
          <Text type="secondary" style={{ fontSize: 12 }}>
            {filtered.length}{kw ? `/${entries.length}` : ''} 行
          </Text>
        </Space>
      </div>

      <div style={{ position: 'relative', flex: 1, minHeight: 0 }}>
        <div
          ref={scrollRef}
          onScroll={onScroll}
          className="terminal-container"
          style={{ position: 'absolute', inset: 0, height, overflowY: 'auto' }}
        >
          {filtered.length === 0 ? (
            <div style={{ padding: '40px 0', textAlign: 'center', opacity: 0.55 }}>
              {kw ? '无匹配日志行' : paused ? '已暂停接收' : '暂无日志输出，等待推送…'}
            </div>
          ) : (
            filtered.map((e) => (
              <div
                key={e.seq}
                className={`log-line ${e.source === 'daemon' ? 'log-daemon' : ''} ${wrap ? '' : 'nowrap'}`}
              >
                {showTs && <span className="log-ts">{fmtTime(e.time)}</span>}
                <span className={`log-lvl ${levelClass(e.level)}`}>{e.level}</span>
                <span className="log-msg">{highlight(e.message || e.raw, kw)}</span>
              </div>
            ))
          )}
        </div>

        {!follow && (
          <Button
            type="primary"
            size="small"
            icon={<VerticalAlignBottomOutlined />}
            onClick={jumpToBottom}
            style={{ position: 'absolute', right: 16, bottom: 16, boxShadow: '0 2px 8px rgba(0,0,0,0.3)' }}
          >
            {newCount > 0 ? `${newCount} 条新日志` : '回到底部'}
          </Button>
        )}
      </div>
    </div>
  );
}
