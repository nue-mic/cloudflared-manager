import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { lazy, Suspense } from 'react';
import { Spin } from 'antd';
import MainLayout from './components/MainLayout';
import Login from './pages/Login';
import Dashboard from './pages/Dashboard';
import Configs from './pages/Configs';
import Logs from './pages/Logs';
import ImportExport from './pages/ImportExport';

const Binaries = lazy(() => import('./pages/Binaries'));
const Traffic = lazy(() => import('./pages/Traffic'));
const Alerts = lazy(() => import('./pages/Alerts'));
const SystemPage = lazy(() => import('./pages/System'));
const ToolsValidate = lazy(() => import('./pages/ToolsValidate'));
const ConfigReference = lazy(() => import('./pages/ConfigReference'));
const Settings = lazy(() => import('./pages/Settings'));
const About = lazy(() => import('./pages/About'));
const CFAccounts = lazy(() => import('./pages/CFAccounts'));
const CFConsole = lazy(() => import('./pages/CFConsole'));

const PageFallback = (
  <div
    style={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      minHeight: 240,
    }}
  >
    <Spin tip="加载中…" size="large" />
  </div>
);

function App() {
  return (
    <BrowserRouter>
      <Suspense fallback={PageFallback}>
        <Routes>
          <Route path="/login" element={<Login />} />

          <Route path="/" element={<MainLayout />}>
            <Route index element={<Navigate to="/dashboard" replace />} />
            <Route path="dashboard" element={<Dashboard />} />
            <Route path="configs" element={<Configs />} />
            <Route path="binaries" element={<Binaries />} />
            <Route path="traffic" element={<Traffic />} />
            <Route path="alerts" element={<Alerts />} />
            <Route path="logs" element={<Logs />} />
            <Route path="system" element={<SystemPage />} />
            <Route path="tools">
              <Route index element={<Navigate to="/tools/validate" replace />} />
              <Route path="validate" element={<ToolsValidate />} />
              <Route path="reference" element={<ConfigReference />} />
            </Route>
            <Route path="cf">
              <Route index element={<Navigate to="/cf/accounts" replace />} />
              <Route path="accounts" element={<CFAccounts />} />
              <Route path="console" element={<CFConsole />} />
            </Route>
            <Route path="import-export" element={<ImportExport />} />
            <Route path="settings" element={<Settings />} />
            <Route path="about" element={<About />} />
            <Route path="*" element={<Navigate to="/dashboard" replace />} />
          </Route>
        </Routes>
      </Suspense>
    </BrowserRouter>
  );
}

export default App;
