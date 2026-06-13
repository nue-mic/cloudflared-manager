// 公共主机名表单字段（CFConsole 与 InstanceCFPanel 共用）。
//
// 仅渲染 Form.Item 字段，需被包裹在外层 <Form> 内使用。字段命名与
// cfIngress.ts 的 PublicHostnameFormValues 对齐：顶部 hostname/path/服务类型/
// 目标地址；折叠面板内为打平的 originRequest 字段（access 用 access_* 前缀）。

import { Form, Input, InputNumber, Select, Switch, Collapse, Space, Typography } from 'antd';
import { SERVICE_TYPES, type ServiceType } from '../../pages/cfIngress';

const { Text } = Typography;

interface Props {
  // 是否展示「同步代理 CNAME」开关（CFConsole 与实例级聚合都需要，故默认展示）。
  showManageDns?: boolean;
  // 目标地址占位符随服务类型变化，靠 watch serviceType 实现。
  serviceTypeWatch?: ServiceType;
}

export default function PublicHostnameFormFields({ showManageDns = true, serviceTypeWatch }: Props) {
  const isHttpStatus = serviceTypeWatch === 'http_status';
  const isUnix = serviceTypeWatch === 'unix';
  const targetPlaceholder = isHttpStatus
    ? '如 404'
    : isUnix
      ? '如 /var/run/app.sock'
      : '如 localhost:8080';

  return (
    <>
      <Form.Item
        label="公共主机名（hostname）"
        name="hostname"
        rules={[{ required: true, message: '请输入完整主机名，如 app.example.com' }]}
        tooltip="用户访问用的完整域名，须为本账号已托管 zone 的子域"
      >
        <Input placeholder="app.example.com" />
      </Form.Item>

      <Form.Item label="路径（path，可选）" name="path" tooltip="可选，按路径前缀路由，如 /api">
        <Input placeholder="/（留空匹配全部路径）" allowClear />
      </Form.Item>

      <Space size={12} style={{ display: 'flex' }} align="start">
        <Form.Item
          label="服务类型"
          name="serviceType"
          rules={[{ required: true, message: '请选择服务类型' }]}
          style={{ width: 200 }}
        >
          <Select
            options={SERVICE_TYPES.map((t) => ({ value: t.value, label: t.label }))}
          />
        </Form.Item>
        <Form.Item
          label="目标地址（service）"
          name="serviceTarget"
          style={{ flex: 1 }}
          tooltip="回源目标，会拼成 cloudflared service 字符串，如 http://localhost:8080"
          rules={[
            {
              required: !isHttpStatus,
              message: '请输入回源目标地址',
            },
          ]}
        >
          <Input placeholder={targetPlaceholder} />
        </Form.Item>
      </Space>

      {showManageDns && (
        <Form.Item
          label="同步代理 CNAME（manage_dns）"
          name="manage_dns"
          valuePropName="checked"
          tooltip="开启后自动在对应 zone 创建/更新指向本隧道的代理 CNAME 记录"
          extra={<Text type="secondary" style={{ fontSize: 12 }}>开启后自动在对应 zone 创建/更新指向本隧道的代理 CNAME 记录</Text>}
        >
          <Switch checkedChildren="自动同步" unCheckedChildren="不同步" />
        </Form.Item>
      )}

      <Collapse
        size="small"
        ghost
        items={[
          {
            key: 'advanced',
            label: '高级 originRequest（全部可选，仅填了的才生效）',
            children: (
              <>
                <Space size={12} style={{ display: 'flex' }} align="start">
                  <Form.Item label="connectTimeout" name="connectTimeout" style={{ flex: 1 }} tooltip="如 30s">
                    <Input placeholder="30s" allowClear />
                  </Form.Item>
                  <Form.Item label="tlsTimeout" name="tlsTimeout" style={{ flex: 1 }} tooltip="如 10s">
                    <Input placeholder="10s" allowClear />
                  </Form.Item>
                </Space>
                <Space size={12} style={{ display: 'flex' }} align="start">
                  <Form.Item label="tcpKeepAlive" name="tcpKeepAlive" style={{ flex: 1 }} tooltip="如 30s">
                    <Input placeholder="30s" allowClear />
                  </Form.Item>
                  <Form.Item label="keepAliveTimeout" name="keepAliveTimeout" style={{ flex: 1 }} tooltip="如 90s">
                    <Input placeholder="90s" allowClear />
                  </Form.Item>
                  <Form.Item label="keepAliveConnections" name="keepAliveConnections" style={{ width: 180 }}>
                    <InputNumber style={{ width: '100%' }} min={0} placeholder="100" />
                  </Form.Item>
                </Space>

                <Space size={20} wrap style={{ marginBottom: 12 }}>
                  <Form.Item label="noHappyEyeballs" name="noHappyEyeballs" valuePropName="checked" style={{ marginBottom: 0 }}>
                    <Switch size="small" />
                  </Form.Item>
                  <Form.Item label="noTLSVerify" name="noTLSVerify" valuePropName="checked" style={{ marginBottom: 0 }}>
                    <Switch size="small" />
                  </Form.Item>
                  <Form.Item label="disableChunkedEncoding" name="disableChunkedEncoding" valuePropName="checked" style={{ marginBottom: 0 }}>
                    <Switch size="small" />
                  </Form.Item>
                  <Form.Item label="http2Origin" name="http2Origin" valuePropName="checked" style={{ marginBottom: 0 }}>
                    <Switch size="small" />
                  </Form.Item>
                </Space>

                <Space size={12} style={{ display: 'flex' }} align="start">
                  <Form.Item label="httpHostHeader" name="httpHostHeader" style={{ flex: 1 }}>
                    <Input placeholder="覆盖回源 Host 头" allowClear />
                  </Form.Item>
                  <Form.Item label="originServerName" name="originServerName" style={{ flex: 1 }} tooltip="回源 TLS SNI">
                    <Input placeholder="origin.example.com" allowClear />
                  </Form.Item>
                </Space>
                <Form.Item label="caPool" name="caPool" tooltip="回源校验用的 CA 证书路径">
                  <Input placeholder="/path/to/ca.pem" allowClear />
                </Form.Item>

                <Space size={12} style={{ display: 'flex' }} align="start">
                  <Form.Item label="proxyType" name="proxyType" style={{ flex: 1 }} tooltip="如 socks">
                    <Input placeholder="socks" allowClear />
                  </Form.Item>
                  <Form.Item label="proxyAddress" name="proxyAddress" style={{ flex: 1 }}>
                    <Input placeholder="127.0.0.1" allowClear />
                  </Form.Item>
                  <Form.Item label="proxyPort" name="proxyPort" style={{ width: 160 }}>
                    <InputNumber style={{ width: '100%' }} min={0} max={65535} placeholder="1080" />
                  </Form.Item>
                </Space>

                <Text type="secondary" style={{ fontSize: 12 }}>Access（保护此主机名）</Text>
                <div style={{ marginTop: 8 }}>
                  <Space size={12} style={{ display: 'flex' }} align="start">
                    <Form.Item label="access.required" name="access_required" valuePropName="checked" style={{ width: 140 }}>
                      <Switch size="small" />
                    </Form.Item>
                    <Form.Item label="access.teamName" name="access_teamName" style={{ flex: 1 }}>
                      <Input placeholder="your-team" allowClear />
                    </Form.Item>
                  </Space>
                  <Form.Item label="access.audTag" name="access_audTag" tooltip="Access 应用的 AUD 标签，可填多个">
                    <Select mode="tags" placeholder="回车输入一个或多个 AUD" tokenSeparators={[',', ' ']} />
                  </Form.Item>
                </div>
              </>
            ),
          },
        ]}
      />
    </>
  );
}
