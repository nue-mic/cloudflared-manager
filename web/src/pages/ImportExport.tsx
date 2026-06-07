import { useState } from 'react';
import { Card, Row, Col, Button, Input, Radio, Form, Upload, App, Typography, Space, Divider } from 'antd';
import {
  CloudDownloadOutlined,
  CloudUploadOutlined,
  CodeOutlined,
  LinkOutlined,
  FileZipOutlined,
  InboxOutlined,
} from '@ant-design/icons';
import client, { getAPIToken } from '../api/client';
import { fmtDate } from '../utils/time';

const { Title, Text, Paragraph } = Typography;
const { Dragger } = Upload;

const ImportExport: React.FC = () => {
  const { message } = App.useApp();
  const [textLoading, setTextLoading] = useState<boolean>(false);
  const [urlLoading, setUrlLoading] = useState<boolean>(false);
  const [allExportLoading, setAllExportLoading] = useState<boolean>(false);

  const [textForm] = Form.useForm();
  const [urlForm] = Form.useForm();

  const handleExportAll = async () => {
    setAllExportLoading(true);
    try {
      const resp = await client.get('/api/v1/export/all', { responseType: 'blob' });
      const blob = new Blob([resp.data], { type: 'application/zip' });
      const downloadUrl = window.URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = downloadUrl;
      link.download = `cfd-configs-backup-${fmtDate(new Date())}.zip`;
      document.body.appendChild(link);
      link.click();
      document.body.removeChild(link);
      window.URL.revokeObjectURL(downloadUrl);
      message.success('整包备份文件已成功导出并下载');
    } catch {
      message.error('导出备份失败，请检查服务状态');
    } finally {
      setAllExportLoading(false);
    }
  };

  const handleImportText = async (values: any) => {
    setTextLoading(true);
    try {
      await client.post('/api/v1/import/text', {
        id: values.id,
        text: values.text,
        format: values.format || 'toml',
      });
      message.success(`文本配置 [${values.id}] 导入成功`);
      textForm.resetFields();
      textForm.setFieldsValue({ format: 'toml' });
    } catch (err: any) {
      message.error('导入失败: ' + (err.response?.data?.error?.message || err.message));
    } finally {
      setTextLoading(false);
    }
  };

  const handleImportURL = async (values: any) => {
    setUrlLoading(true);
    try {
      await client.post('/api/v1/import/url', { id: values.id, url: values.url });
      message.success(`URL 远程拉取配置 [${values.id}] 成功`);
      urlForm.resetFields();
    } catch (err: any) {
      message.error('导入失败: ' + (err.response?.data?.error?.message || err.message));
    } finally {
      setUrlLoading(false);
    }
  };

  const zipDraggerProps = {
    name: 'file',
    multiple: false,
    showUploadList: false,
    headers: { Authorization: `Bearer ${getAPIToken()}` },
    action: '/api/v1/import/zip',
    onChange(info: any) {
      const { status } = info.file;
      if (status === 'done') {
        message.success(`${info.file.name} ZIP 备份包恢复成功`);
      } else if (status === 'error') {
        message.error(`${info.file.name} 备份包解析失败，请确认文件格式`);
      }
    },
  };

  const fileUploadProps = (id: string, onSuccess: () => void) => ({
    name: 'file',
    multiple: false,
    showUploadList: false,
    headers: { Authorization: `Bearer ${getAPIToken()}` },
    action: `/api/v1/import/file?id=${encodeURIComponent(id)}`,
    beforeUpload(_: any) {
      if (!id) {
        message.warning('请先填写导入后的配置文件唯一 ID');
        return Upload.LIST_IGNORE;
      }
      return true;
    },
    onChange(info: any) {
      if (info.file.status === 'done') {
        message.success(`${info.file.name} 配置文件导入成功`);
        onSuccess();
      } else if (info.file.status === 'error') {
        message.error(`${info.file.name} 导入失败，请检查文件编码或网络`);
      }
    },
  });

  const [singleFileId, setSingleFileId] = useState<string>('');

  const cardStyle: React.CSSProperties = { height: '100%', borderRadius: 10 };

  return (
    <div>
      <Title level={4} style={{ marginBottom: 16 }}>配置备份与导入导出</Title>

      <Row gutter={[16, 16]}>
        <Col xs={24} md={12}>
          <Card
            title={<Space><FileZipOutlined /> 全量备份数据管理</Space>}
            style={cardStyle}
          >
            <Paragraph type="secondary">
              一键导出当前管理器中的全部 cloudflared 配置为标准 ZIP 包；也可拖拽上传以前导出的 ZIP 备份来覆盖还原。
            </Paragraph>

            <div style={{ margin: '20px 0', textAlign: 'center' }}>
              <Button
                type="primary"
                size="large"
                icon={<CloudDownloadOutlined />}
                loading={allExportLoading}
                onClick={handleExportAll}
              >
                生成并下载全量备份 (.zip)
              </Button>
            </div>

            <Divider plain>上传还原备份包</Divider>

            <Dragger {...zipDraggerProps}>
              <p className="ant-upload-drag-icon">
                <InboxOutlined />
              </p>
              <p className="ant-upload-text">点击或拖拽 ZIP 备份文件到这里进行还原</p>
              <p className="ant-upload-hint">
                整包还原将<b>覆盖</b>当前系统中所有同名配置，请提前做好记录。
              </p>
            </Dragger>
          </Card>
        </Col>

        <Col xs={24} md={12}>
          <Card title={<Space><CodeOutlined /> 粘贴文本配置导入</Space>} style={cardStyle}>
            <Form form={textForm} layout="vertical" onFinish={handleImportText}>
              <Row gutter={16}>
                <Col span={14}>
                  <Form.Item
                    label="保存 ID 标识 (纯英文数字)"
                    name="id"
                    rules={[{ required: true, message: '请输入唯一ID标识' }]}
                  >
                    <Input placeholder="例如: office_linux" />
                  </Form.Item>
                </Col>
                <Col span={10}>
                  <Form.Item label="代码格式" name="format" initialValue="toml">
                    <Radio.Group buttonStyle="solid">
                      <Radio.Button value="toml">TOML</Radio.Button>
                      <Radio.Button value="ini">INI</Radio.Button>
                    </Radio.Group>
                  </Form.Item>
                </Col>
              </Row>

              <Form.Item
                label="配置文件内容"
                name="text"
                rules={[{ required: true, message: '请输入配置代码' }]}
              >
                <Input.TextArea
                  rows={8}
                  placeholder={'[common]\nserver_addr = x.x.x.x\nserver_port = 7000\nauth.token = abcde'}
                  style={{ fontFamily: 'ui-monospace, "Fira Code", monospace', fontSize: 13 }}
                />
              </Form.Item>

              <Form.Item style={{ marginBottom: 0, textAlign: 'right' }}>
                <Button type="primary" htmlType="submit" loading={textLoading}>
                  一键导入配置
                </Button>
              </Form.Item>
            </Form>
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} md={12}>
          <Card title={<Space><CloudUploadOutlined /> 导入本地配置文件</Space>} style={cardStyle}>
            <Paragraph type="secondary">
              从本地选择现有的 cloudflared 配置文件（.yaml/.yml）上传并导入到管理器中。
            </Paragraph>

            <Space direction="vertical" style={{ width: '100%', marginTop: 8 }}>
              <Text>输入要保存的配置 ID 标识：</Text>
              <Input
                placeholder="例如: test_server"
                value={singleFileId}
                onChange={(e) => setSingleFileId(e.target.value)}
              />

              <div style={{ marginTop: 8 }}>
                <Upload {...fileUploadProps(singleFileId, () => setSingleFileId(''))}>
                  <Button type="primary" icon={<CloudUploadOutlined />} disabled={!singleFileId}>
                    选择文件并上传导入
                  </Button>
                </Upload>
                {!singleFileId && (
                  <div style={{ marginTop: 4 }}>
                    <Text type="secondary" style={{ fontSize: 12 }}>* 请先填写上方 ID 标识后激活上传按钮。</Text>
                  </div>
                )}
              </div>
            </Space>
          </Card>
        </Col>

        <Col xs={24} md={12}>
          <Card title={<Space><LinkOutlined /> 从远程 URL 下载导入</Space>} style={cardStyle}>
            <Form form={urlForm} layout="vertical" onFinish={handleImportURL}>
              <Form.Item
                label="保存 ID 标识 (纯英文数字)"
                name="id"
                rules={[{ required: true, message: '请输入唯一ID标识' }]}
              >
                <Input placeholder="例如: remote_mac" />
              </Form.Item>

              <Form.Item
                label="配置远程下载 URL (HTTP / HTTPS)"
                name="url"
                rules={[
                  { required: true, message: '请输入下载链接' },
                  { type: 'url', message: '请输入有效的网络地址' },
                ]}
              >
                <Input placeholder="http://example.com/frps.toml" />
              </Form.Item>

              <Form.Item style={{ marginBottom: 0, textAlign: 'right' }}>
                <Button type="primary" htmlType="submit" loading={urlLoading}>
                  发起远程下载并导入
                </Button>
              </Form.Item>
            </Form>
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default ImportExport;
