import { useMemo, useState } from 'react';
import {
  Card, Space, Typography, Tag, Table, Input, Switch, Button, Alert, Divider, Empty, Row, Col, App,
  theme as antdTheme,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { TableProps } from 'antd';
import type { Key } from 'react';
import {
  ReadOutlined, CopyOutlined, SearchOutlined, ThunderboltOutlined,
  SafetyCertificateOutlined, ToolOutlined, ClearOutlined, FileTextOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';

import {
  CATALOG, EXTRA_ALLOWED_ENV, RESERVED_ENV, modelledEnvKeys,
  fieldSnippet, buildDraftYaml, FULL_EXAMPLE, MINIMAL_EXAMPLE,
  type FieldDef,
} from './configCatalog';

const { Title, Text, Paragraph } = Typography;
const MONO = `'Cascadia Code', Consolas, 'SF Mono', Menlo, ui-monospace, monospace`;

const ConfigReference: React.FC = () => {
  const { token } = antdTheme.useToken();
  const { message } = App.useApp();
  const navigate = useNavigate();

  const [query, setQuery] = useState('');
  const [showAdvanced, setShowAdvanced] = useState(true);
  const [selected, setSelected] = useState<string[]>([]); // 选入草稿的字段 path

  const copy = (s: string, tip = '已复制') => {
    navigator.clipboard.writeText(s).then(
      () => message.success(tip),
      () => message.error('复制失败，请手动选择'),
    );
  };

  const q = query.trim().toLowerCase();
  const groups = useMemo(() => {
    return CATALOG.map((g) => ({
      ...g,
      fields: g.fields.filter(
        (f) =>
          (showAdvanced || !f.advanced) &&
          (q === '' ||
            f.path.toLowerCase().includes(q) ||
            f.desc.toLowerCase().includes(q) ||
            (f.allowed || '').toLowerCase().includes(q) ||
            (f.env || '').toLowerCase().includes(q)),
      ),
    })).filter((g) => g.fields.length > 0);
  }, [q, showAdvanced]);

  const draftYaml = useMemo(() => buildDraftYaml(selected), [selected]);

  // rowSelection：仅在「当前可见行」范围内增删，过滤掉的已选字段保持不变。
  const rowSelectionFor = (renderedFields: FieldDef[]): TableProps<FieldDef>['rowSelection'] => {
    const visible = new Set(renderedFields.map((f) => f.path));
    return {
      columnTitle: '草稿',
      columnWidth: 56,
      selectedRowKeys: renderedFields.filter((f) => selected.includes(f.path)).map((f) => f.path),
      onChange: (keys: Key[]) => {
        setSelected((prev) => [
          ...prev.filter((p) => !visible.has(p)),
          ...keys.map(String),
        ]);
      },
    };
  };

  const columns: ColumnsType<FieldDef> = [
    {
      title: '字段',
      dataIndex: 'path',
      key: 'path',
      width: 200,
      render: (path: string, f) => (
        <Space size={4} wrap>
          <Text code copyable={{ text: path, tooltips: ['复制键名', '已复制'] }} style={{ fontFamily: MONO, fontSize: 12.5 }}>
            {path}
          </Text>
          {f.advanced && <Tag style={{ marginInlineStart: 0 }}>高级</Tag>}
        </Space>
      ),
    },
    {
      title: '类型',
      dataIndex: 'type',
      key: 'type',
      width: 120,
      responsive: ['lg'],
      render: (t: string) => <Tag color="blue" style={{ fontFamily: MONO, fontSize: 11.5 }}>{t}</Tag>,
    },
    {
      title: '允许值 / 默认',
      key: 'allowed',
      width: 210,
      render: (_, f) => (
        <div style={{ fontSize: 12.5, lineHeight: 1.6 }}>
          {f.allowed && <div>{f.allowed}</div>}
          <div>
            <Text type="secondary" style={{ fontSize: 12 }}>默认：</Text>
            <Text style={{ fontFamily: MONO, fontSize: 12 }}>{f.default || '—'}</Text>
          </div>
          {f.constraint && <div><Text type="warning" style={{ fontSize: 11.5 }}>约束：{f.constraint}</Text></div>}
        </div>
      ),
    },
    {
      title: '对应 env',
      dataIndex: 'env',
      key: 'env',
      width: 190,
      responsive: ['xl'],
      render: (e: string) =>
        e && e.startsWith('TUNNEL_')
          ? <Text code style={{ fontFamily: MONO, fontSize: 11.5 }}>{e}</Text>
          : <Text type="secondary" style={{ fontSize: 12 }}>{e || '—'}</Text>,
    },
    {
      title: '说明',
      dataIndex: 'desc',
      key: 'desc',
      render: (d: string) => <Text style={{ fontSize: 12.5 }}>{d}</Text>,
    },
    {
      title: '',
      key: 'op',
      width: 72,
      align: 'right',
      render: (_, f) => {
        const g = groups.find((gg) => gg.fields.includes(f)) || groups[0];
        return (
          <Button
            size="small"
            type="text"
            icon={<CopyOutlined />}
            onClick={() => copy(fieldSnippet(g, f), `已复制 ${f.path} 片段`)}
            title="复制该字段 YAML 片段"
          >
            片段
          </Button>
        );
      },
    },
  ];

  const CodeBlock: React.FC<{ code: string; copyTip?: string; maxHeight?: number }> = ({ code, copyTip, maxHeight }) => (
    <div style={{ position: 'relative' }}>
      <Button size="small" icon={<CopyOutlined />} onClick={() => copy(code, copyTip)} style={{ position: 'absolute', top: 8, right: 8, zIndex: 1 }}>
        复制
      </Button>
      <pre
        style={{
          margin: 0, padding: '14px 16px', background: '#0b0f14', color: '#cdd6e4',
          borderRadius: 8, fontFamily: MONO, fontSize: 12.5, lineHeight: 1.65,
          overflowX: 'auto', maxHeight, overflowY: maxHeight ? 'auto' : undefined,
          border: `1px solid ${token.colorBorderSecondary}`,
        }}
      >
        {code}
      </pre>
    </div>
  );

  const modelled = modelledEnvKeys();

  const draftPanel = (
    <Card
      title={<Space><FileTextOutlined /> 草稿 YAML · 实时预览</Space>}
      extra={<Tag color={selected.length ? 'processing' : 'default'}>{selected.length} 字段</Tag>}
      style={{ borderRadius: 10 }}
      styles={{ header: { background: token.colorFillTertiary } }}
    >
      {draftYaml ? (
        <>
          <CodeBlock code={draftYaml} copyTip="已复制草稿 YAML" maxHeight={420} />
          <Space wrap style={{ marginTop: 12 }}>
            <Button type="primary" icon={<CopyOutlined />} onClick={() => copy(draftYaml, '已复制草稿 YAML')}>复制草稿</Button>
            <Button icon={<ToolOutlined />} onClick={() => navigate('/tools/validate', { state: { yaml: draftYaml } })}>去校验这段草稿</Button>
            <Button danger type="text" icon={<ClearOutlined />} onClick={() => setSelected([])}>清空</Button>
          </Space>
          <Paragraph type="secondary" style={{ fontSize: 12, marginTop: 10, marginBottom: 0 }}>
            勾选左侧每个字段的「草稿」复选框即可拼装；token 为占位符，记得替换为真实令牌。
          </Paragraph>
        </>
      ) : (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={<Text type="secondary" style={{ fontSize: 13 }}>勾选左侧字段的「草稿」复选框，自动拼装并在此实时预览 YAML。</Text>}
        />
      )}
    </Card>
  );

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      {/* Hero */}
      <Card styles={{ body: { padding: 0 } }} style={{ borderRadius: 12, overflow: 'hidden', border: `1px solid ${token.colorBorderSecondary}` }}>
        <div style={{ padding: '28px 28px', background: 'linear-gradient(135deg, #0f172a 0%, #1e3a8a 45%, #6d28d9 100%)', color: '#fff' }}>
          <Space size={14} align="center">
            <div style={{ width: 52, height: 52, borderRadius: 13, background: 'rgba(255,255,255,0.18)', border: '1px solid rgba(255,255,255,0.3)', display: 'inline-flex', alignItems: 'center', justifyContent: 'center' }}>
              <ReadOutlined style={{ fontSize: 28, color: '#fff' }} />
            </div>
            <div>
              <Title level={2} style={{ color: '#fff', margin: 0, fontWeight: 700 }}>配置参考 · YAML 参数速查</Title>
              <Text style={{ color: 'rgba(255,255,255,0.85)', fontSize: 13.5 }}>cloudflared 隧道（token 模式）全部可配参数，逐字段可复制 / 可拼装草稿</Text>
            </div>
          </Space>
        </div>
      </Card>

      {/* 关键提示 */}
      <Alert
        type="info"
        showIcon
        message="token 模式速记"
        description={
          <ul style={{ margin: '4px 0 0', paddingInlineStart: 18, fontSize: 13, lineHeight: 1.8 }}>
            <li><b>token 必填</b>：启动前校验长度 100–1500、base64 字符集；其余字段<b>留空 = 用 cloudflared 默认</b>。</li>
            <li><b>ingress / 公开主机名 / origin</b> 在 Cloudflare Zero Trust dashboard 配置，<b>不在这里</b>。</li>
            <li>结构是<b>嵌套</b>的（edge / reliability / logging / identity），别拍平成顶层键，否则保存 400。</li>
            <li>键名大小写敏感：<Text code style={{ fontFamily: MONO }}>edgeIpVersion</Text>（小写 p）、<Text code style={{ fontFamily: MONO }}>postQuantum</Text>、<Text code style={{ fontFamily: MONO }}>gracePeriod</Text>。</li>
          </ul>
        }
      />

      {/* 工具栏 */}
      <Card styles={{ body: { padding: 12 } }} style={{ borderRadius: 10 }}>
        <Space wrap size={12} style={{ width: '100%', justifyContent: 'space-between' }}>
          <Space wrap size={12}>
            <Input allowClear prefix={<SearchOutlined />} placeholder="搜索字段 / env / 说明…" value={query} onChange={(e) => setQuery(e.target.value)} style={{ width: 260 }} />
            <Space size={6}>
              <Switch checked={showAdvanced} onChange={setShowAdvanced} size="small" />
              <Text type="secondary" style={{ fontSize: 13 }}>显示高级参数</Text>
            </Space>
          </Space>
          <Space wrap size={8}>
            <Button icon={<CopyOutlined />} onClick={() => copy(MINIMAL_EXAMPLE, '已复制最小示例')}>复制最小示例</Button>
            <Button type="primary" icon={<CopyOutlined />} onClick={() => copy(FULL_EXAMPLE, '已复制完整示例')}>复制完整示例 YAML</Button>
            <Button icon={<ToolOutlined />} onClick={() => navigate('/tools/validate')}>去校验</Button>
          </Space>
        </Space>
      </Card>

      {/* 完整示例 */}
      <Card title={<Space><ThunderboltOutlined /> 完整示例 YAML（覆盖全部参数）</Space>} style={{ borderRadius: 10 }} styles={{ header: { background: token.colorFillTertiary } }}>
        <CodeBlock code={FULL_EXAMPLE} copyTip="已复制完整示例" />
      </Card>

      {/* 主体：左参数表 + 右草稿（窄屏自动堆叠） */}
      <Row gutter={16}>
        <Col xs={24} lg={15}>
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            {groups.map((g) => (
              <Card
                key={g.key}
                title={
                  <Space>
                    <SafetyCertificateOutlined style={{ color: token.colorPrimary }} />
                    <span>{g.title}</span>
                    {g.yamlKey && <Tag color="purple" style={{ fontFamily: MONO }}>{g.yamlKey}:</Tag>}
                  </Space>
                }
                extra={<Text type="secondary" style={{ fontSize: 12 }}>{g.fields.length} 项</Text>}
                style={{ borderRadius: 10 }}
                styles={{ header: { background: token.colorFillTertiary } }}
              >
                <Paragraph type="secondary" style={{ marginTop: -4, marginBottom: 12, fontSize: 13 }}>{g.desc}</Paragraph>
                <Table<FieldDef>
                  size="small"
                  rowKey="path"
                  columns={columns}
                  dataSource={g.fields}
                  pagination={false}
                  scroll={{ x: 'max-content' }}
                  rowSelection={rowSelectionFor(g.fields)}
                />
              </Card>
            ))}
            {groups.length === 0 && (
              <Card style={{ borderRadius: 10, textAlign: 'center', padding: 24 }}>
                <Text type="secondary">没有匹配「{query}」的字段。</Text>
              </Card>
            )}
          </Space>
        </Col>
        <Col xs={24} lg={9}>
          <div style={{ position: 'sticky', top: 16 }}>{draftPanel}</div>
        </Col>
      </Row>

      {/* env 白名单 */}
      <Card title={<Space><ThunderboltOutlined /> advancedEnvOverrides · 可用与保留的 env</Space>} style={{ borderRadius: 10 }} styles={{ header: { background: token.colorFillTertiary } }}>
        <Paragraph type="secondary" style={{ fontSize: 13 }}>
          逃生舱：当某 cloudflared 变量未被上面字段建模时可在此直接注入。<b>非白名单键会在启动时被静默丢弃</b>（「配置校验」会给出警告）。
        </Paragraph>

        <Text strong style={{ fontSize: 13 }}>① 已建模字段对应的 env（也可经此重复设置）</Text>
        <div style={{ margin: '8px 0 16px' }}>
          <Space wrap size={[6, 8]}>
            {modelled.map((k) => (
              <Tag key={k} color="blue" style={{ fontFamily: MONO, fontSize: 11.5, cursor: 'pointer' }} onClick={() => copy(k, '已复制 ' + k)}>{k}</Tag>
            ))}
          </Space>
        </div>

        <Text strong style={{ fontSize: 13 }}>② 额外放行（未建模，高级用途）</Text>
        <div style={{ margin: '8px 0 16px' }}>
          <Space direction="vertical" size={6} style={{ width: '100%' }}>
            {EXTRA_ALLOWED_ENV.map((e) => (
              <div key={e.key}>
                <Tag color="geekblue" style={{ fontFamily: MONO, fontSize: 11.5, cursor: 'pointer' }} onClick={() => copy(e.key, '已复制 ' + e.key)}>{e.key}</Tag>
                <Text type="secondary" style={{ fontSize: 12.5 }}>{e.desc}</Text>
              </div>
            ))}
          </Space>
        </div>

        <Text strong style={{ fontSize: 13 }}>③ 保留键（cfdmgrd 自管，无法覆盖）</Text>
        <div style={{ margin: '8px 0 16px' }}>
          <Space wrap size={[6, 8]}>
            {RESERVED_ENV.map((k) => (<Tag key={k} color="red" style={{ fontFamily: MONO, fontSize: 11.5 }}>{k}</Tag>))}
          </Space>
        </div>

        <Divider style={{ margin: '8px 0 14px' }} />
        <Text strong style={{ fontSize: 13 }}>示例片段</Text>
        <div style={{ marginTop: 8 }}>
          <CodeBlock code={`advancedEnvOverrides:\n  TUNNEL_DNS_RESOLVER_ADDRS: 1.1.1.1,1.0.0.1\n  TUNNEL_METRICS_UPDATE_FREQ: 5s\n`} copyTip="已复制 advancedEnvOverrides 示例" />
        </div>
      </Card>

      <Text type="secondary" style={{ fontSize: 12, display: 'block', textAlign: 'center', paddingBottom: 8 }}>
        字段权威来源：pkg/cfdflags（registry / mapping / whitelist） + pkg/cfdconfig（tunnel / validate）。与「配置校验」页配合使用。
      </Text>
    </Space>
  );
};

export default ConfigReference;
