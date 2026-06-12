import { useEffect, useState } from 'react';
import { Timeline, Empty, Tag, Typography } from 'antd';
import { useEventSubscription } from '../../events/EventStreamContext';
import type { BusEvent, InstanceStateData, InstanceErrorData, AlertData } from '../../events/types';
import { fmtTime } from '../../utils/time';

const { Text } = Typography;

interface Item {
  seq: number;
  ts: string;
  color: string;
  label: string;
  detail?: string;
}

const STATE_ZH: Record<string, string> = {
  started: '已启动',
  stopped: '已停止',
  starting: '启动中',
  stopping: '停止中',
};

interface Props {
  id: string;
}

/**
 * InstanceEvents —— 本实例生命周期事件时间线。复用全局 WS /events 订阅，按
 * config_id 过滤；为会话级（自打开此面板起累积，不回放历史），零额外后端。
 */
export default function InstanceEvents({ id }: Props) {
  const [items, setItems] = useState<Item[]>([]);

  useEffect(() => {
    setItems([]);
  }, [id]);

  useEventSubscription(['instance.state', 'instance.error', 'config.changed', 'alert'], (e: BusEvent) => {
    if (e.config_id && e.config_id !== id) return;
    let it: Item | null = null;
    if (e.type === 'instance.state') {
      const st = (e.data as InstanceStateData | undefined)?.state ?? '';
      it = {
        seq: e.seq,
        ts: e.ts,
        color: st === 'started' ? 'green' : st === 'stopped' ? 'red' : 'blue',
        label: `状态 → ${STATE_ZH[st] ?? st}`,
      };
    } else if (e.type === 'instance.error') {
      it = { seq: e.seq, ts: e.ts, color: 'red', label: '错误', detail: (e.data as InstanceErrorData | undefined)?.message };
    } else if (e.type === 'config.changed') {
      it = { seq: e.seq, ts: e.ts, color: 'blue', label: '配置已变更' };
    } else if (e.type === 'alert') {
      const d = e.data as AlertData | undefined;
      it = {
        seq: e.seq,
        ts: e.ts,
        color: d?.state === 'firing' ? 'red' : 'green',
        label: d?.state === 'firing' ? '告警触发' : '告警恢复',
        detail: d?.rule_name,
      };
    }
    if (it) {
      const next = it;
      setItems((prev) => [next, ...prev].slice(0, 100));
    }
  });

  if (items.length === 0) {
    return <Empty description="本会话暂无事件（自打开此面板起累积）" style={{ padding: '48px 0' }} />;
  }

  return (
    <Timeline
      items={items.map((it) => ({
        key: it.seq,
        color: it.color,
        children: (
          <div>
            <Text type="secondary" style={{ fontSize: 12, marginRight: 8 }}>
              {fmtTime(it.ts)}
            </Text>
            <Tag color={it.color}>{it.label}</Tag>
            {it.detail && (
              <div>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {it.detail}
                </Text>
              </div>
            )}
          </div>
        ),
      }))}
    />
  );
}
