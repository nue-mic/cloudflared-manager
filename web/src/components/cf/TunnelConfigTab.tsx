// 隧道「全局配置」Tab：全局 originRequest + warp-routing.enabled。
// 编辑后保留 ingress 不变，整体 putTunnelConfig。

import { useCallback, useEffect, useState } from 'react';
import {
  Card,
  Form,
  Input,
  InputNumber,
  Switch,
  Button,
  Space,
  Collapse,
  Skeleton,
  Typography,
  App,
} from 'antd';
import { SaveOutlined, ReloadOutlined } from '@ant-design/icons';
import { cfApi } from '../../api/client';
import type { CFTunnelConfig } from '../../api/types';
import {
  buildOriginRequest,
  originRequestToForm,
  type OriginRequestFormValues,
} from '../../pages/cfIngress';

const { Text } = Typography;

interface GlobalFormValues extends OriginRequestFormValues {
  warpRouting?: boolean;
}

interface Props {
  aid: string;
  tid: string;
}

export default function TunnelConfigTab({ aid, tid }: Props) {
  const { message } = App.useApp();
  const [form] = Form.useForm<GlobalFormValues>();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  // 保留原始 config 的 ingress，保存时原样回填。
  const [baseConfig, setBaseConfig] = useState<CFTunnelConfig | null>(null);

  const errMsg = (err: unknown): string => {
    const e = err as { response?: { data?: { error?: { message?: string } } }; message?: string };
    return e.response?.data?.error?.message || e.message || '未知错误';
  };

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await cfApi.getTunnelConfig(aid, tid);
      const cfg = resp.data?.config ?? {};
      setBaseConfig(cfg);
      form.resetFields();
      form.setFieldsValue({
        ...originRequestToForm(cfg.originRequest),
        warpRouting: !!cfg['warp-routing']?.enabled,
      });
    } catch (err: unknown) {
      message.error('加载隧道配置失败：' + errMsg(err));
    } finally {
      setLoading(false);
    }
  }, [aid, tid, form, message]);

  useEffect(() => {
    load();
  }, [load]);

  const save = async () => {
    const values = await form.validateFields();
    setSaving(true);
    try {
      const next: CFTunnelConfig = { ...(baseConfig ?? {}) };
      const or = buildOriginRequest(values);
      if (or) next.originRequest = or;
      else delete next.originRequest;
      next['warp-routing'] = { enabled: !!values.warpRouting };
      const resp = await cfApi.putTunnelConfig(aid, tid, next);
      setBaseConfig(resp.data?.config ?? next);
      message.success('隧道配置已保存');
    } catch (err: unknown) {
      message.error('保存失败：' + errMsg(err));
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <Skeleton active />;

  return (
    <Card
      size="small"
      style={{ borderRadius: 10 }}
      title="全局配置（originRequest + WARP 路由）"
      extra={
        <Space>
          <Button size="small" icon={<ReloadOutlined />} onClick={load}>重新加载</Button>
          <Button size="small" type="primary" icon={<SaveOutlined />} loading={saving} onClick={save}>
            保存
          </Button>
        </Space>
      }
    >
      <Form form={form} layout="vertical" requiredMark="optional">
        <Form.Item
          label="WARP 路由（warp-routing.enabled）"
          name="warpRouting"
          valuePropName="checked"
          tooltip="启用后可经隧道访问私有网段（需配合 Cloudflare WARP 客户端）"
        >
          <Switch checkedChildren="启用" unCheckedChildren="关闭" />
        </Form.Item>

        <Text type="secondary" style={{ fontSize: 12 }}>
          以下为全局 originRequest 默认值，单个公共主机名可在其表单中覆盖。仅填了的字段才会下发。
        </Text>

        <Collapse
          size="small"
          ghost
          style={{ marginTop: 8 }}
          defaultActiveKey={['or']}
          items={[
            {
              key: 'or',
              label: '全局 originRequest',
              children: (
                <>
                  <Space size={12} style={{ display: 'flex' }} align="start">
                    <Form.Item label="connectTimeout" name="connectTimeout" style={{ flex: 1 }}>
                      <Input placeholder="30s" allowClear />
                    </Form.Item>
                    <Form.Item label="tlsTimeout" name="tlsTimeout" style={{ flex: 1 }}>
                      <Input placeholder="10s" allowClear />
                    </Form.Item>
                  </Space>
                  <Space size={12} style={{ display: 'flex' }} align="start">
                    <Form.Item label="tcpKeepAlive" name="tcpKeepAlive" style={{ flex: 1 }}>
                      <Input placeholder="30s" allowClear />
                    </Form.Item>
                    <Form.Item label="keepAliveTimeout" name="keepAliveTimeout" style={{ flex: 1 }}>
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
                    <Form.Item label="originServerName" name="originServerName" style={{ flex: 1 }}>
                      <Input placeholder="回源 TLS SNI" allowClear />
                    </Form.Item>
                  </Space>
                  <Form.Item label="caPool" name="caPool">
                    <Input placeholder="/path/to/ca.pem" allowClear />
                  </Form.Item>
                  <Space size={12} style={{ display: 'flex' }} align="start">
                    <Form.Item label="proxyType" name="proxyType" style={{ flex: 1 }}>
                      <Input placeholder="socks" allowClear />
                    </Form.Item>
                    <Form.Item label="proxyAddress" name="proxyAddress" style={{ flex: 1 }}>
                      <Input placeholder="127.0.0.1" allowClear />
                    </Form.Item>
                    <Form.Item label="proxyPort" name="proxyPort" style={{ width: 160 }}>
                      <InputNumber style={{ width: '100%' }} min={0} max={65535} placeholder="1080" />
                    </Form.Item>
                  </Space>
                </>
              ),
            },
          ]}
        />
      </Form>
    </Card>
  );
}
