// 公共主机名 新建 / 编辑 Modal（CFConsole 与 InstanceCFPanel 共用）。
//
// 仅负责表单收集与回填；提交逻辑由父组件通过 onSubmit 注入（CFConsole 直改远端
// 隧道配置 + 自行同步 DNS；InstanceCFPanel 走实例级聚合 API）。
//
// 公共主机名仿 Cloudflare 官方后台拆成「子域前缀 + 域名(zone)可搜索下拉」：
// 打开时按 aid 拉该账号的 zone 列表填充下拉；编辑时把已有 hostname 按 zone 列表
// 拆回 subdomain/zoneName；提交前由 buildHostname 合成完整 hostname。

import { useEffect, useState } from 'react';
import { Modal, Form } from 'antd';
import PublicHostnameFormFields from './PublicHostnameFormFields';
import { cfApi } from '../../api/client';
import type { CFZone } from '../../api/types';
import { splitHostname, buildHostname, type PublicHostnameFormValues, type ServiceType } from '../../pages/cfIngress';

interface Props {
  open: boolean;
  // 该公共主机名所属账号的 store id（listZones 用）。CFConsole 传 aid；实例面板传 binding.account_id。
  aid: string;
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
  aid,
  initial,
  title,
  showManageDns = true,
  onCancel,
  onSubmit,
}: Props) {
  const [form] = Form.useForm<PublicHostnameFormValues>();
  const [submitting, setSubmitting] = useState(false);
  const [zones, setZones] = useState<CFZone[]>([]);
  const [zonesLoading, setZonesLoading] = useState(false);
  const serviceType = Form.useWatch('serviceType', form) as ServiceType | undefined;
  const zoneName = Form.useWatch('zoneName', form) as string | undefined;
  const subdomain = Form.useWatch('subdomain', form) as string | undefined;

  // 打开时拉该账号的 zone 列表（域名下拉数据源）。
  useEffect(() => {
    if (!open || !aid) return;
    setZonesLoading(true);
    cfApi
      .listZones(aid)
      .then((r) => setZones(r.data?.items || []))
      .catch(() => setZones([]))
      .finally(() => setZonesLoading(false));
  }, [open, aid]);

  // initial 由父组件内联构造（publicHostnameToForm / ingressRuleToForm），父级每次重渲染
  // 都是一个新对象。实例详情页在实例「运行中」时每 5s 拉 live 状态会重渲染 InstanceCFPanel，
  // 若把高频变化的 initial 列入下面 effect 的依赖，就会每 5s 跑一次 resetFields/setFieldsValue
  // —— 表单被重置、打开着的域名下拉随之关闭、编辑中的输入被清空。
  // 解法：初始化只依赖 open。effect 闭包会捕获「打开那一刻」的 initial；之后的重渲染因 deps
  // 不变不再执行该 effect，下拉与输入得以保持。故意不把 initial 列入依赖（见 eslint-disable）。

  // 打开时设置基础表单值（新建用默认；编辑回填 initial 的非 hostname 字段）。
  useEffect(() => {
    if (!open) return;
    form.resetFields();
    if (initial) {
      form.setFieldsValue(initial);
    } else {
      form.setFieldsValue({ serviceType: 'http', manage_dns: true } as PublicHostnameFormValues);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, form]);

  // 编辑：待 zone 列表就绪后，把已有 hostname 拆成 subdomain/zoneName 填进下拉。
  // 依赖 zones——zones 从 [] 变为已加载时会再跑一次，确保拆分用上完整列表；同样不依赖 initial 身份。
  useEffect(() => {
    if (!open || !initial) return;
    const { subdomain: sub, zoneName: zn } = splitHostname(initial.hostname || '', zones);
    form.setFieldsValue({ subdomain: sub, zoneName: zn } as PublicHostnameFormValues);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, zones, form]);

  const handleOk = async () => {
    let values: PublicHostnameFormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }
    // 合成完整 hostname 供下游 formToPayload/formToIngressRule 使用。
    values.hostname = buildHostname(values.subdomain, values.zoneName);
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
      width={900}
      style={{ top: 40 }}
      styles={{ body: { maxHeight: 'calc(100vh - 220px)', overflowY: 'auto', paddingRight: 12 } }}
    >
      <Form form={form} layout="vertical" style={{ marginTop: 8 }}>
        <PublicHostnameFormFields
          showManageDns={showManageDns}
          serviceTypeWatch={serviceType}
          zones={zones}
          zonesLoading={zonesLoading}
          zoneNameWatch={zoneName}
          subdomainWatch={subdomain}
        />
      </Form>
    </Modal>
  );
}
