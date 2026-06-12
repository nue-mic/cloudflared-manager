import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { Row, Col, Card, Segmented, Empty, Alert, Skeleton, Space, Typography, theme as antdTheme } from 'antd';
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip as RTooltip,
  ResponsiveContainer,
  CartesianGrid,
} from 'recharts';
import { isAxiosError } from 'axios';
import { metricsApi } from '../../api/client';
import { fmtHourMinute, fmtTime } from '../../utils/time';

const { Text } = Typography;

const RANGES = [
  { key: '1h', label: '1 小时', sec: 3600, step: 30 },
  { key: '6h', label: '6 小时', sec: 21_600, step: 120 },
  { key: '24h', label: '24 小时', sec: 86_400, step: 300 },
] as const;
type RangeKey = (typeof RANGES)[number]['key'];

const REFRESH_MS = 30_000;

interface ChartRow {
  t: number;
  reqRate: number;
  errRate: number;
  errPct: number;
  conns: number;
}

function fmtVal(n: number): string {
  if (!isFinite(n)) return '0';
  return n.toFixed(n >= 100 ? 0 : n >= 10 ? 1 : 2);
}

interface Props {
  id: string;
  running: boolean;
}

export default function InstanceMetrics({ id, running }: Props) {
  const { token } = antdTheme.useToken();
  const [range, setRange] = useState<RangeKey>('1h');
  const [rows, setRows] = useState<ChartRow[]>([]);
  const [loading, setLoading] = useState(false);
  const [disabled, setDisabled] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const timer = useRef<number | undefined>(undefined);

  const cfg = RANGES.find((r) => r.key === range) ?? RANGES[0];

  const load = useCallback(async (instId: string, step: number, sec: number) => {
    const to = Math.floor(Date.now() / 1000);
    const from = to - sec;
    try {
      const resp = await metricsApi.traffic(instId, { scope: 'server', from, to, step });
      const pts = resp.data.points ?? [];
      setRows(
        pts.map((p) => ({
          t: p.ts * 1000,
          reqRate: step > 0 ? p.in / step : 0,
          errRate: step > 0 ? p.out / step : 0,
          errPct: p.in > 0 ? (p.out / p.in) * 100 : 0,
          conns: p.conns,
        }))
      );
      setDisabled(false);
      setErr(null);
    } catch (e) {
      if (isAxiosError(e) && e.response?.status === 503) {
        setDisabled(true);
        setRows([]);
      } else if (isAxiosError(e)) {
        setErr((e.response?.data as { message?: string } | undefined)?.message || e.message);
      } else {
        setErr(String(e));
      }
    }
  }, []);

  useEffect(() => {
    let stop = false;
    const run = async () => {
      if (stop) return;
      setLoading(true);
      await load(id, cfg.step, cfg.sec);
      if (!stop) setLoading(false);
    };
    void run();
    timer.current = window.setInterval(() => void run(), REFRESH_MS);
    return () => {
      stop = true;
      if (timer.current) clearInterval(timer.current);
    };
  }, [id, cfg.step, cfg.sec, load]);

  const chartCard = (title: ReactNode, key: keyof ChartRow, color: string, unit: string, gid: string) => (
    <Card title={title} size="small" styles={{ body: { padding: 12 } }} style={{ borderRadius: 10 }}>
      {rows.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该时段无数据" style={{ padding: '28px 0' }} />
      ) : (
        <ResponsiveContainer width="100%" height={170}>
          <AreaChart data={rows}>
            <defs>
              <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor={color} stopOpacity={0.5} />
                <stop offset="95%" stopColor={color} stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke={token.colorBorderSecondary} />
            <XAxis
              dataKey="t"
              tickFormatter={(t) => fmtHourMinute(Number(t))}
              stroke={token.colorTextSecondary}
              fontSize={11}
              minTickGap={28}
            />
            <YAxis stroke={token.colorTextSecondary} fontSize={11} width={44} tickFormatter={(v) => fmtVal(Number(v))} />
            <RTooltip
              labelFormatter={(v) => fmtTime(Number(v))}
              formatter={(v) => [`${fmtVal(Number(v ?? 0))}${unit ? ' ' + unit : ''}`, '']}
              contentStyle={{ background: token.colorBgElevated, border: 'none', borderRadius: 8 }}
            />
            <Area type="monotone" dataKey={key} stroke={color} fill={`url(#${gid})`} isAnimationActive={false} />
          </AreaChart>
        </ResponsiveContainer>
      )}
    </Card>
  );

  return (
    <Space direction="vertical" size={12} style={{ width: '100%' }}>
      <Space style={{ justifyContent: 'space-between', width: '100%' }} wrap>
        <Text type="secondary" style={{ fontSize: 12 }}>
          请求 / 错误 / HA 连接（非字节流量），每 {REFRESH_MS / 1000}s 刷新
        </Text>
        <Segmented value={range} onChange={(v) => setRange(v as RangeKey)} options={RANGES.map((r) => ({ label: r.label, value: r.key }))} />
      </Space>

      {disabled && <Alert type="warning" showIcon message="指标存储未启用" />}
      {err && <Alert type="error" showIcon message="加载失败" description={err} closable onClose={() => setErr(null)} />}
      {!running && rows.length === 0 && !disabled && (
        <Alert type="info" showIcon message="实例未运行，仅展示历史时段（运行中才持续采集）" />
      )}

      {loading && rows.length === 0 ? (
        <Skeleton active />
      ) : (
        <Row gutter={[12, 12]}>
          <Col xs={24} xl={12}>{chartCard('请求速率（req/s）', 'reqRate', token.colorPrimary, 'req/s', 'mReq')}</Col>
          <Col xs={24} xl={12}>{chartCard('错误速率（err/s）', 'errRate', token.colorError, 'err/s', 'mErr')}</Col>
          <Col xs={24} xl={12}>{chartCard('错误率（%）', 'errPct', token.colorWarning, '%', 'mPct')}</Col>
          <Col xs={24} xl={12}>{chartCard('HA 连接数', 'conns', token.colorSuccess, '', 'mConn')}</Col>
        </Row>
      )}
    </Space>
  );
}
