// 公共主机名 新建 / 编辑 Modal（CFConsole 与 InstanceCFPanel 共用）。
//
// 仅负责表单收集与回填；提交逻辑由父组件通过 onSubmit 注入（CFConsole 直改远端
// 隧道配置 + 自行同步 DNS；InstanceCFPanel 走实例级聚合 API）。

import { useEffect, useState } from 'react';
import { Modal, Form } from 'antd';
import PublicHostnameFormFields from './PublicHostnameFormFields';
import type { PublicHostnameFormValues } from '../../pages/cfIngress';
import type { ServiceType } from '../../pages/cfIngress';

interface Props {
  open: boolean;
  // 编辑时的初始值；新建传 undefined。
  initial?: PublicHostnameFormValues;
  title: string;
  // 是否展示「同步代理 CNAME」开关。
  showManageDns?: boolean;
  onCancel: () => void;
  // 返回 Promise，resolve 后 Modal 自动关闭；reject/throw 时保持打开。
  onSubmit: (values: PublicHostnameFormValues) => Promise<void>;
}

export default function PublicHostnameModal({
  open,
  initial,
  title,
  showManageDns = true,
  onCancel,
  onSubmit,
}: Props) {
  const [form] = Form.useForm<PublicHostnameFormValues>();
  const [submitting, setSubmitting] = useState(false);
  const serviceType = Form.useWatch('serviceType', form) as ServiceType | undefined;

  useEffect(() => {
    if (!open) return;
    form.resetFields();
    if (initial) {
      form.setFieldsValue(initial);
    } else {
      form.setFieldsValue({ serviceType: 'http', manage_dns: true } as PublicHostnameFormValues);
    }
  }, [open, initial, form]);

  const handleOk = async () => {
    let values: PublicHostnameFormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    setSubmitting(true);
    try {
      await onSubmit(values);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      title={title}
      open={open}
      onOk={handleOk}
      confirmLoading={submitting}
      onCancel={onCancel}
      okText="保存"
      cancelText="取消"
      destroyOnClose
      width={680}
    >
      <Form form={form} layout="vertical" requiredMark="optional" style={{ marginTop: 8 }}>
        <PublicHostnameFormFields showManageDns={showManageDns} serviceTypeWatch={serviceType} />
      </Form>
    </Modal>
  );
}
