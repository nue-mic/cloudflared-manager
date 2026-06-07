import axios from 'axios';
import type {
  ConfigList,
  ConfigEnvelope,
  ValidateResp,
  BinaryList,
  BinaryMeta,
  BinaryItem,
} from './types';

// localStorage key
const TOKEN_KEY = 'cfdmgr_api_token';

export const getAPIToken = (): string => {
  return localStorage.getItem(TOKEN_KEY) || '';
};

export const setAPIToken = (token: string) => {
  localStorage.setItem(TOKEN_KEY, token);
};

export const clearAPIToken = () => {
  localStorage.removeItem(TOKEN_KEY);
};

const client = axios.create({
  baseURL: '',
  timeout: 30000,
});

// 请求拦截器：自动注入 Bearer Token
client.interceptors.request.use(
  (config) => {
    const token = getAPIToken();
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
  },
  (error) => Promise.reject(error)
);

// 响应拦截器：统一处理 401
client.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response && error.response.status === 401) {
      if (
        !error.config.url?.includes('/api/v1/version') &&
        !error.config.url?.includes('/api/v1/health')
      ) {
        clearAPIToken();
        window.location.href = '/login';
      }
    }
    return Promise.reject(error);
  }
);

export default client;

// ── Configs API ──────────────────────────────────────────────────────────────

export const configsApi = {
  list: () => client.get<ConfigList>('/api/v1/configs'),
  get: (id: string) => client.get<ConfigEnvelope>(`/api/v1/configs/${id}`),
  create: (payload: object) => client.post('/api/v1/configs', payload),
  update: (id: string, payload: object) => client.put(`/api/v1/configs/${id}`, payload),
  delete: (id: string) => client.delete(`/api/v1/configs/${id}`),
  start: (id: string) => client.post(`/api/v1/configs/${id}/start`),
  stop: (id: string) => client.post(`/api/v1/configs/${id}/stop`),
  reload: (id: string) => client.post(`/api/v1/configs/${id}/reload`),
  duplicate: (id: string, newId: string) =>
    client.post(`/api/v1/configs/${id}/duplicate`, { new_id: newId }),
  status: (id: string) => client.get(`/api/v1/configs/${id}/status`),
};

// ── Binaries API ─────────────────────────────────────────────────────────────

export const binariesApi = {
  list: () => client.get<BinaryList>('/api/v1/binaries'),
  available: () => client.get<BinaryMeta>('/api/v1/binaries/available'),
  install: (version: string) =>
    client.post<BinaryItem>('/api/v1/binaries/install', { version }),
  activate: (version: string) =>
    client.post('/api/v1/binaries/activate', { version }),
  delete: (version: string) => client.delete(`/api/v1/binaries/${encodeURIComponent(version)}`),
};

// ── Validate API ─────────────────────────────────────────────────────────────
export const validateApi = {
  validate: (content: string) =>
    client.post<ValidateResp>('/api/v1/validate', content, {
      headers: { 'Content-Type': 'text/plain' },
    }),
};
