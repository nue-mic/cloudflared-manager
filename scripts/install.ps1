#Requires -Version 5.1
# =============================================================================
# cfdmgrd 一键安装脚本 (cloudflared-manager) — Windows / PowerShell 版
#
#   支持: Windows 10/11 / Windows Server (amd64 / arm64)
#   服务: 通过 NSSM 将 cfdmgrd.exe 包装为真正的 Windows 服务 (可在 services.msc 管理)
#   功能: 自动识别架构 -> 下载二进制 -> 安装 -> 注册服务 -> 开机自启 -> 健康检查
#
# 一行安装 (推荐, 管理员 PowerShell 中执行):
#   irm https://raw.githubusercontent.com/mia-clark/cloudflared-manager/main/scripts/install.ps1 | iex
#
# 非交互 / 自定义示例 (先把脚本下到本地):
#   powershell -ExecutionPolicy Bypass -File install.ps1 -Yes -Port 9000 -Token mysecret
#   powershell -ExecutionPolicy Bypass -File install.ps1 -Port random
#   powershell -ExecutionPolicy Bypass -File install.ps1 -Uninstall
#
# 环境变量 (等价于参数, 便于自动化):
#   $env:CFDM_PORT=9000; $env:CFDM_API_TOKEN='xxx'; $env:CFDM_VERSION='v1.2.14'; $env:ASSUME_YES=1
# =============================================================================

[CmdletBinding()]
param(
    [Alias('p')][string]$Port    = $env:CFDM_PORT,
    [Alias('t')][string]$Token   = $env:CFDM_API_TOKEN,
    [Alias('v')][string]$Version = $env:CFDM_VERSION,
    [Alias('y')][switch]$Yes,
    [Alias('u')][switch]$Update,
    [Alias('f')][switch]$Force,
    [switch]$Uninstall,
    [string]$Proxy   = $env:CFDM_DOWNLOAD_PROXY,
    [switch]$NoProxy,
    [Alias('h')][switch]$Help
)

if (-not $NoProxy -and $env:CFDM_NO_PROXY -eq '1') { $NoProxy = $true }

$ErrorActionPreference = 'Stop'

# ----------------------------------------------------------------------------
# 常量配置
# ----------------------------------------------------------------------------
$Repo         = 'mia-clark/cloudflared-manager'
$BinName      = 'cfdmgrd.exe'
$ServiceName  = 'cfdmgrd'
$DisplayName  = 'cfdmgrd - cloudflared multi-instance manager'
$DefaultPort  = '8080'
$InstallDir   = Join-Path $env:ProgramFiles 'cfdmgrd'        # 二进制 + nssm.exe
$DataDir      = Join-Path $env:ProgramData  'cfdmgrd\data'   # 运行数据
$LogDir       = Join-Path $env:ProgramData  'cfdmgrd\logs'   # 服务日志
$NssmVersion  = '2.24'
$NssmZipUrl   = "https://nssm.cc/release/nssm-$NssmVersion.zip"

# GitHub release 下载代理候选 (按用户指定顺序: 公开 4 家在前, 自建 6 家在后)
#   URL 拼装: "$proxy$githubUrl"; 安装时遍历, 第一个能下载并通过 Expand-Archive
#   验证的就用; 全部代理失败回落直连。与姊妹仓库 frpc-manager 共享同一份代理列表。
$DlProxies = @(
    'https://gh-proxy.com/',
    'https://ghfast.top/',
    'https://github.tbedu.top/',
    'https://gh.idayer.com/',
    'https://docker.srv1.qzz.io/',
    'https://dk-proxy.srv1.qzz.io/',
    'https://dk-proxy.966788.xyz/',
    'https://dk-proxy.srv0.qzz.io/',
    'https://docker.srv0.qzz.io/',
    'https://docker.966788.xyz/'
)

# 运行期填充
$script:Arch        = ''
$script:BinPath     = Join-Path $InstallDir $BinName
$script:NssmPath    = Join-Path $InstallDir 'nssm.exe'
$script:TokenSource = ''
$script:TmpDir      = ''

if ($env:ASSUME_YES -eq '1') { $Yes = $true }

# ----------------------------------------------------------------------------
# 输出辅助 (带颜色)
# ----------------------------------------------------------------------------
function Write-Info { param([string]$m) Write-Host '[*] ' -ForegroundColor Blue   -NoNewline; Write-Host $m }
function Write-Ok   { param([string]$m) Write-Host '[+] ' -ForegroundColor Green  -NoNewline; Write-Host $m }
function Write-Warn { param([string]$m) Write-Host '[!] ' -ForegroundColor Yellow -NoNewline; Write-Host $m }
function Write-Err  { param([string]$m) Write-Host '[x] ' -ForegroundColor Red    -NoNewline; Write-Host $m }
function Die        { param([string]$m) Write-Err $m; Cleanup; exit 1 }

function Cleanup {
    if ($script:TmpDir -and (Test-Path $script:TmpDir)) {
        Remove-Item -Recurse -Force $script:TmpDir -ErrorAction SilentlyContinue
    }
}

# ----------------------------------------------------------------------------
# 帮助
# ----------------------------------------------------------------------------
function Show-Usage {
    Write-Host @"
cfdmgrd 一键安装脚本 (Windows)

用法: powershell -ExecutionPolicy Bypass -File install.ps1 [选项]

选项:
  -Port <端口>     指定监听端口; 传 "random" 表示随机端口; 省略则交互/默认 $DefaultPort
  -Token <令牌>    指定 API 令牌; 省略则交互输入, 留空则生成强随机令牌
  -Version <版本>  指定版本 (如 v1.2.14); 省略则安装最新版
  -Yes             非交互模式, 端口用默认值、令牌自动随机生成
  -Update          全自动更新到最新版 (保留现有端口/令牌/数据, 仅换二进制并重启)
  -Force           配合 -Update: 即使已是最新版也强制重装
  -Uninstall       卸载 (停止/删除服务 + 删除二进制)
  -Proxy <URL>     指定单一下载代理 (如 https://my.mirror/), 跳过内置数组
  -NoProxy         跳过所有代理, 直连 GitHub 下载
  -Help            显示帮助

示例:
  install.ps1                              # 全交互: 逐项询问端口/令牌
  install.ps1 -Port 9000                   # 指定端口, 仅询问令牌
  install.ps1 -Yes -Port 9000 -Token secret  # 完全静默安装
  install.ps1 -Port random                 # 随机端口
  install.ps1 -Version v1.2.14 -Port 8888  # 指定版本+端口
  install.ps1 -Update                      # 全自动更新到最新版
  install.ps1 -Update -Force               # 强制重装当前最新版
  install.ps1 -Uninstall                   # 卸载
  install.ps1 -NoProxy                     # 跳过代理直连 GitHub

下载策略:
  默认按内置代理数组挨个尝试 (公开代理 4 家在前, 自建 6 家在后), 第一个能
  下载并解开为合法 zip 的就用; 全部代理失败回落直连 GitHub。
"@
}

# ----------------------------------------------------------------------------
# 管理员检测 + 自动 UAC 自提权
# ----------------------------------------------------------------------------
function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    (New-Object Security.Principal.WindowsPrincipal $id).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Assert-Admin {
    if (Test-Admin) { return }

    # 仅在以本地脚本文件运行时才能自提权; 管道 (irm|iex) 场景拿不到脚本路径
    if ($PSCommandPath) {
        Write-Info '需要管理员权限, 正在尝试通过 UAC 提权重新运行...'
        $argList = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"")
        foreach ($kv in $PSBoundParameters.GetEnumerator()) {
            if ($kv.Value -is [switch]) {
                if ($kv.Value.IsPresent) { $argList += "-$($kv.Key)" }
            } else {
                $argList += "-$($kv.Key)"; $argList += "`"$($kv.Value)`""
            }
        }
        try {
            Start-Process -FilePath (Get-Process -Id $PID).Path -Verb RunAs -ArgumentList $argList
        } catch {
            Die '提权被取消或失败。请右键“以管理员身份运行” PowerShell 后重试。'
        }
        exit 0
    }

    Die '需要管理员权限。请在【管理员 PowerShell】中运行本脚本 (右键 PowerShell -> 以管理员身份运行)。'
}

# ----------------------------------------------------------------------------
# 网络初始化 (启用 TLS1.2, 关闭进度条以加速下载)
# ----------------------------------------------------------------------------
function Initialize-Net {
    try {
        [Net.ServicePointManager]::SecurityProtocol = `
            [Net.SecurityProtocolType]::Tls12 -bor [Net.SecurityProtocolType]::Tls11
    } catch { }
    $script:OldProgress = $ProgressPreference
    $global:ProgressPreference = 'SilentlyContinue'
}

# ----------------------------------------------------------------------------
# 平台探测: 架构
# ----------------------------------------------------------------------------
function Get-Platform {
    switch ($env:PROCESSOR_ARCHITECTURE) {
        'AMD64' { $script:Arch = 'amd64' }
        'ARM64' { $script:Arch = 'arm64' }
        'x86'   {
            # WOW64：64 位系统上的 32 位进程，以系统真实架构为准；纯 32 位系统用 386
            if ([Environment]::Is64BitOperatingSystem) {
                if ($env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { $script:Arch = 'arm64' }
                else { $script:Arch = 'amd64' }
            } else {
                $script:Arch = '386'
            }
        }
        default {
            if ([Environment]::Is64BitOperatingSystem) { $script:Arch = 'amd64' }
            else { $script:Arch = '386' }
        }
    }
    Write-Info "检测到平台: windows/$($script:Arch)"
}

# ----------------------------------------------------------------------------
# 交互读取: 返回输入值, 非交互/静默则用默认值
# ----------------------------------------------------------------------------
function Read-Prompt {
    param([string]$Message, [string]$Default = '')
    if ($Yes) { return $Default }
    if ($Default) { $hint = " [$Default]" } else { $hint = '' }
    $r = Read-Host -Prompt "? $Message$hint"
    if ([string]::IsNullOrEmpty($r)) { return $Default }
    return $r
}

# ----------------------------------------------------------------------------
# 生成随机令牌 / 随机端口 / 端口校验
# ----------------------------------------------------------------------------
function New-Token {
    $bytes = New-Object byte[] 24
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    -join ($bytes | ForEach-Object { $_.ToString('x2') })
}

function New-RandomPort { Get-Random -Minimum 20000 -Maximum 60000 }

function Test-Port {
    param([string]$P)
    if ($P -notmatch '^\d+$') { return $false }
    $n = [int]$P
    return ($n -ge 1 -and $n -le 65535)
}

# ----------------------------------------------------------------------------
# 下载文件
# ----------------------------------------------------------------------------
function Get-RemoteFile {
    param([string]$Url, [string]$Dest)
    Invoke-WebRequest -Uri $Url -OutFile $Dest -UseBasicParsing `
        -Headers @{ 'User-Agent' = 'cfdmgrd-installer' } -TimeoutSec 30
}

# 验证下载文件是合法 zip (防"伪 200": 代理返回 HTML 错误页但 HTTP 200)
# 用 Expand-Archive 试解到临时目录, 成功 = 真包; 失败 = 伪 200
function Test-Zip {
    param([string]$Path)
    if (-not (Test-Path $Path) -or (Get-Item $Path).Length -eq 0) { return $false }
    $probe = Join-Path $env:TEMP ("zipprobe_" + [Guid]::NewGuid().ToString('N'))
    try {
        Expand-Archive -Path $Path -DestinationPath $probe -Force -ErrorAction Stop
        return $true
    } catch {
        return $false
    } finally {
        if (Test-Path $probe) { Remove-Item -Recurse -Force $probe -ErrorAction SilentlyContinue }
    }
}

# 智能代理下载: 遍历 $DlProxies, 第一个成功+合法的就用; 全失败回落直连
# 优先级: -Proxy/$env:CFDM_DOWNLOAD_PROXY > 内置数组 > 直连
function Invoke-Download {
    param([string]$GhUrl, [string]$Dest)

    if ($Proxy) {
        $p = $Proxy.TrimEnd('/') + '/'
        Write-Info "使用指定代理: $p"
        try { Get-RemoteFile -Url ($p + $GhUrl) -Dest $Dest } catch { }
        if (Test-Zip $Dest) { return $true }
        Write-Warn '指定代理失败/返回非法包, 回落直连'
        Remove-Item -Force $Dest -ErrorAction SilentlyContinue
    } elseif (-not $NoProxy) {
        foreach ($p in $DlProxies) {
            Write-Info "尝试代理: $p"
            try { Get-RemoteFile -Url ($p + $GhUrl) -Dest $Dest } catch { Remove-Item -Force $Dest -ErrorAction SilentlyContinue; continue }
            if (Test-Zip $Dest) {
                Write-Ok "下载源: $p"
                return $true
            }
            Write-Warn '  -> 返回非法包 (伪 200?), 跳下一家'
            Remove-Item -Force $Dest -ErrorAction SilentlyContinue
        }
        Write-Warn '全部代理失败, 回落直连 GitHub'
    }

    # 直连兜底
    Write-Info "直连: $GhUrl"
    try { Get-RemoteFile -Url $GhUrl -Dest $Dest } catch { return $false }
    if (-not (Test-Zip $Dest)) { Write-Err '直连下载的文件也不是合法 zip'; return $false }
    return $true
}

# ----------------------------------------------------------------------------
# 解析目标版本 (GitHub API), 失败则提示手动指定
# ----------------------------------------------------------------------------
function Resolve-Version {
    if ($Version) {
        if ($Version -notmatch '^v') { $script:Version = "v$Version" } else { $script:Version = $Version }
        Write-Info "使用指定版本: $($script:Version)"
        return
    }
    Write-Info '正在查询最新版本...'
    try {
        $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" `
            -Headers @{ 'User-Agent' = 'cfdmgrd-installer' } -UseBasicParsing
        $script:Version = $rel.tag_name
    } catch {
        Die '无法获取最新版本, 请用 -Version 手动指定 (如 -Version v1.2.14)'
    }
    if (-not $script:Version) { Die '无法解析最新版本号, 请用 -Version 手动指定' }
    Write-Ok "最新版本: $($script:Version)"
}

# ----------------------------------------------------------------------------
# 决定端口与令牌 (交互 / 默认 / 随机)
# ----------------------------------------------------------------------------
function Resolve-Port {
    if ($Port -eq 'random') {
        $script:Port = "$(New-RandomPort)"
        Write-Ok "已生成随机端口: $($script:Port)"
        return
    }
    if (-not $Port) {
        $script:Port = Read-Prompt "请输入监听端口 (回车=默认 $DefaultPort, 输入 r=随机)" $DefaultPort
    } else {
        $script:Port = $Port
    }
    if ($script:Port -eq 'r' -or $script:Port -eq 'random') {
        $script:Port = "$(New-RandomPort)"
        Write-Ok "已生成随机端口: $($script:Port)"
    }
    if (-not (Test-Port $script:Port)) { Die "端口非法: '$($script:Port)' (应为 1-65535)" }
    Write-Info "监听端口: $($script:Port)"
}

function Resolve-Token {
    if ($Token) {
        $script:Token = $Token
        $script:TokenSource = '命令行/环境变量指定'
    } elseif (-not $Yes) {
        $r = Read-Prompt '请输入 API 令牌 (后台访问凭证, 回车=自动生成强随机令牌)' ''
        if ($r) { $script:Token = $r; $script:TokenSource = '手动输入' }
    }
    if (-not $script:Token) {
        $script:Token = New-Token
        $script:TokenSource = '自动生成'
        Write-Ok '已自动生成强随机 API 令牌'
    } else {
        Write-Info "API 令牌: $($script:TokenSource)"
    }
}

# ----------------------------------------------------------------------------
# 安装前确认
# ----------------------------------------------------------------------------
function Confirm-Install {
    Write-Host ''
    Write-Host '即将安装, 请确认以下信息:' -ForegroundColor White
    Write-Host ("  平台      : windows/{0}" -f $script:Arch)
    Write-Host ("  版本      : {0}" -f $script:Version)
    Write-Host ("  监听端口  : {0}" -f $script:Port)
    Write-Host ("  API 令牌  : {0}  ({1})" -f $script:Token, $script:TokenSource)
    Write-Host ("  安装目录  : {0}" -f $script:BinPath)
    Write-Host ("  数据目录  : {0}" -f $DataDir)
    Write-Host ''
    if ($Yes) { return }
    $r = Read-Prompt '确认继续? [Y/n]' 'Y'
    if ($r -match '^(n|no)$') { Die '已取消安装' }
}

# ----------------------------------------------------------------------------
# 下载并安装 cfdmgrd 二进制
# ----------------------------------------------------------------------------
function Install-Binary {
    $verNum = $script:Version.TrimStart('v')
    $asset  = "cfdmgrd_${verNum}_windows_$($script:Arch).zip"
    $url    = "https://github.com/$Repo/releases/download/$($script:Version)/$asset"

    $script:TmpDir = Join-Path $env:TEMP ("cfdmgr_" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $script:TmpDir | Out-Null

    $zipPath = Join-Path $script:TmpDir $asset
    Write-Info "目标: $url"
    if (-not (Invoke-Download -GhUrl $url -Dest $zipPath)) {
        Die '全部下载途径失败 (代理 + 直连), 请检查网络或版本号'
    }

    Write-Info '解压安装包...'
    $extractDir = Join-Path $script:TmpDir 'extract'
    Expand-Archive -Path $zipPath -DestinationPath $extractDir -Force
    $exe = Get-ChildItem -Path $extractDir -Filter $BinName -Recurse | Select-Object -First 1
    if (-not $exe) { Die "安装包中未找到 $BinName" }

    Write-Info "安装到 $($script:BinPath)"
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item -Path $exe.FullName -Destination $script:BinPath -Force
    try {
        $ver = & $script:BinPath version 2>$null
        Write-Ok "二进制安装完成: $ver"
    } catch {
        Write-Ok "二进制安装完成: $($script:BinPath)"
    }
}

# ----------------------------------------------------------------------------
# 下载并就绪 NSSM (服务包装器)
# ----------------------------------------------------------------------------
function Install-Nssm {
    if (Test-Path $script:NssmPath) { return }   # 已存在则复用
    Write-Info "下载服务管理器 NSSM v$NssmVersion ..."
    if (-not $script:TmpDir) {
        $script:TmpDir = Join-Path $env:TEMP ("cfdmgr_" + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Force -Path $script:TmpDir | Out-Null
    }
    $nssmZip = Join-Path $script:TmpDir 'nssm.zip'
    try { Get-RemoteFile -Url $NssmZipUrl -Dest $nssmZip } catch { Die "NSSM 下载失败 ($NssmZipUrl): $_" }

    $nssmDir = Join-Path $script:TmpDir 'nssm'
    Expand-Archive -Path $nssmZip -DestinationPath $nssmDir -Force
    # NSSM 仅提供 win32/win64; arm64 通过 x64 模拟运行 win64 版本
    $sub = if ([Environment]::Is64BitOperatingSystem) { 'win64' } else { 'win32' }
    $src = Get-ChildItem -Path $nssmDir -Filter 'nssm.exe' -Recurse |
        Where-Object { $_.DirectoryName -like "*\$sub" } | Select-Object -First 1
    if (-not $src) { $src = Get-ChildItem -Path $nssmDir -Filter 'nssm.exe' -Recurse | Select-Object -First 1 }
    if (-not $src) { Die 'NSSM 压缩包中未找到 nssm.exe' }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item -Path $src.FullName -Destination $script:NssmPath -Force
    Write-Ok 'NSSM 就绪'
}

# 服务是否已存在
function Test-Service {
    return [bool](Get-Service -Name $ServiceName -ErrorAction SilentlyContinue)
}

# 静默移除已存在的服务 (用于重装前清理)
function Remove-ServiceIfExists {
    if (Test-Service) {
        & $script:NssmPath stop $ServiceName 2>$null | Out-Null
        & $script:NssmPath remove $ServiceName confirm 2>$null | Out-Null
        Start-Sleep -Milliseconds 500
    }
}

# ----------------------------------------------------------------------------
# 注册 / 配置服务 (NSSM)
# ----------------------------------------------------------------------------
function Register-CfdmgrService {
    Write-Info "注册 Windows 服务: $ServiceName"
    New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
    New-Item -ItemType Directory -Force -Path $LogDir  | Out-Null

    Remove-ServiceIfExists

    & $script:NssmPath install $ServiceName $script:BinPath serve | Out-Null
    & $script:NssmPath set $ServiceName DisplayName  $DisplayName | Out-Null
    & $script:NssmPath set $ServiceName Description   "cfdmgrd - headless cloudflared multi-instance manager daemon" | Out-Null
    & $script:NssmPath set $ServiceName AppDirectory  $InstallDir | Out-Null
    & $script:NssmPath set $ServiceName Start         'SERVICE_AUTO_START' | Out-Null

    # 环境变量注入 (等价于 systemd EnvironmentFile)
    $envPairs = @(
        "CFDM_API_TOKEN=$($script:Token)",
        "CFDM_HTTP_ADDR=:$($script:Port)",
        "CFDM_DATA_DIR=$DataDir",
        "CFDM_LOG_LEVEL=info",
        "CFDM_CORS_ORIGINS=*",
        "CFDM_DOCS_ENABLED=true",
        "CFDM_SELF_UPDATE_ENABLED=true"
    )
    & $script:NssmPath set $ServiceName AppEnvironmentExtra @envPairs | Out-Null

    # 日志与崩溃自动重启
    & $script:NssmPath set $ServiceName AppStdout   (Join-Path $LogDir 'cfdmgrd.log') | Out-Null
    & $script:NssmPath set $ServiceName AppStderr   (Join-Path $LogDir 'cfdmgrd.log') | Out-Null
    & $script:NssmPath set $ServiceName AppRotateFiles 1 | Out-Null
    & $script:NssmPath set $ServiceName AppRotateBytes 10485760 | Out-Null
    & $script:NssmPath set $ServiceName AppExit Default Restart | Out-Null
    & $script:NssmPath set $ServiceName AppRestartDelay 5000 | Out-Null

    & $script:NssmPath start $ServiceName | Out-Null
    Write-Ok '服务已注册、启动并设置为开机自启'
}

# ----------------------------------------------------------------------------
# 生成统一管理命令 cfm (cfm.cmd + cfm.ps1), 并把安装目录加入系统 PATH
#   之后在任意终端 (cmd / PowerShell) 都可直接执行 cfm <命令>
# ----------------------------------------------------------------------------
function Install-Cli {
    Write-Info '安装管理命令: cfm'
    $cliPs1 = Join-Path $InstallDir 'cfm.ps1'
    $cliCmd = Join-Path $InstallDir 'cfm.cmd'
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

    # 头部: 注入安装期常量 (可展开 here-string; 用反引号转义运行期 $ 以保留字面量)
    $head = @"
# cfm.ps1 — cfdmgrd 管理命令 (由 install.ps1 自动生成, 请勿手动编辑)
`$ServiceName = '$ServiceName'
`$InstallDir  = '$InstallDir'
`$BinName     = '$BinName'
`$DataDir     = '$DataDir'
`$LogDir      = '$LogDir'
`$Repo        = '$Repo'
"@

    # 主体: 运行期逻辑 (字面 here-string, 内容原样写入生成文件)
    $body = @'
$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = [Text.Encoding]::UTF8 } catch { }

$BinPath  = Join-Path $InstallDir $BinName
$NssmPath = Join-Path $InstallDir 'nssm.exe'
$LogFile  = Join-Path $LogDir 'cfdmgrd.log'
$RawUrl   = "https://raw.githubusercontent.com/$Repo/main/scripts/install.ps1"
# 允许用镜像源覆盖 install.ps1 下载地址 (适配国内网络)
if ($env:CFDM_INSTALL_URL) { $RawUrl = $env:CFDM_INSTALL_URL }

$AllArgs = @($args)
$Cmd  = if ($AllArgs.Count -ge 1) { $AllArgs[0] } else { 'help' }
$Rest = if ($AllArgs.Count -gt 1) { $AllArgs[1..($AllArgs.Count - 1)] } else { @() }

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    (New-Object Security.Principal.WindowsPrincipal $id).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)
}
function Need-Admin {
    if (Test-Admin) { return }
    Write-Host '[*] 该操作需要管理员权限, 正在通过 UAC 提权...' -ForegroundColor Blue
    $a = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"") + $AllArgs
    Start-Process -FilePath (Get-Process -Id $PID).Path -Verb RunAs -ArgumentList $a
    exit 0
}
function Use-Nssm { Test-Path $NssmPath }

function Do-Start   { Need-Admin; if (Use-Nssm) { & $NssmPath start $ServiceName } else { & sc.exe start $ServiceName }; Write-Host '[+] 服务已启动' -ForegroundColor Green }
function Do-Stop    { Need-Admin; if (Use-Nssm) { & $NssmPath stop $ServiceName } else { & sc.exe stop $ServiceName }; Write-Host '[+] 服务已停止' -ForegroundColor Green }
function Do-Restart { Need-Admin; if (Use-Nssm) { & $NssmPath restart $ServiceName } else { & sc.exe stop $ServiceName; Start-Sleep -Seconds 1; & sc.exe start $ServiceName }; Write-Host '[+] 服务已重启' -ForegroundColor Green }
function Do-Status  { $s = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue; if ($s) { $s | Format-Table -AutoSize Status, Name, DisplayName } else { Write-Host '[!] 服务未安装' -ForegroundColor Yellow } }
function Do-Enable  { Need-Admin; if (Use-Nssm) { & $NssmPath set $ServiceName Start SERVICE_AUTO_START | Out-Null } else { & sc.exe config $ServiceName start= auto | Out-Null }; Write-Host '[+] 已设置开机自启' -ForegroundColor Green }
function Do-Disable { Need-Admin; if (Use-Nssm) { & $NssmPath set $ServiceName Start SERVICE_DEMAND_START | Out-Null } else { & sc.exe config $ServiceName start= demand | Out-Null }; Write-Host '[+] 已取消开机自启' -ForegroundColor Green }

function Do-Logs {
    if (-not (Test-Path $LogFile)) { Write-Host "[x] 未找到日志文件: $LogFile" -ForegroundColor Red; exit 1 }
    $follow = $false
    foreach ($r in $Rest) { if ($r -in @('-f', '--follow', 'follow')) { $follow = $true } }
    if ($follow) { Get-Content -Path $LogFile -Tail 200 -Wait } else { Get-Content -Path $LogFile -Tail 200 }
}
function Write-CliPanel {
    Write-Host '────────────────────────────────────────────'
    Write-Host '  管理命令 (已加入 PATH, 新开终端任意目录可用):'
    $rows = @(
        @('cfm start',     '启动服务'),
        @('cfm stop',      '停止服务'),
        @('cfm restart',   '重启服务'),
        @('cfm status',    '查看状态'),
        @('cfm logs -f',   '实时日志'),
        @('cfm info',      '查看完整信息'),
        @('cfm config',    '查看/编辑配置'),
        @('cfm update',    '更新到最新版'),
        @('cfm uninstall', '卸载'),
        @('cfm help',      '查看全部命令')
    )
    foreach ($r in $rows) { Write-Host ('    {0,-13} # {1}' -f $r[0], $r[1]) }
    Write-Host '────────────────────────────────────────────'
}
# ----------------------------------------------------------------------------
# 外网 IP 探测 (与 install.ps1 主体同款, 此处独立嵌入让 cfm 自包含)
# ----------------------------------------------------------------------------
$PubIpV4Urls = @(
    'https://4.ipw.cn', 'https://api.ip.sb/ip', 'https://api.ipify.org',
    'https://ifconfig.me/ip', 'https://ipv4.icanhazip.com',
    'http://members.3322.org/dyndns/getip'
)
$PubIpV6Urls = @('https://6.ipw.cn', 'https://ipv6.icanhazip.com')

function Get-PublicIps {
    $found = New-Object System.Collections.Generic.HashSet[string]
    foreach ($u in $PubIpV4Urls) {
        try {
            $r = Invoke-RestMethod -Uri $u -TimeoutSec 2 -UseBasicParsing -ErrorAction Stop
            if ($r) {
                $m = ([string]$r -replace '\s', '') | Select-String -Pattern '([0-9]{1,3}\.){3}[0-9]{1,3}' -AllMatches
                if ($m.Matches.Count -gt 0) { [void]$found.Add($m.Matches[0].Value) }
            }
        } catch { }
    }
    foreach ($u in $PubIpV6Urls) {
        try {
            $r = Invoke-RestMethod -Uri $u -TimeoutSec 2 -UseBasicParsing -ErrorAction Stop
            if ($r) {
                $s = ([string]$r -replace '\s', '')
                if ($s -match '^[0-9a-fA-F:]+$' -and $s -match ':') { [void]$found.Add($s) }
            }
        } catch { }
    }
    return @($found)
}

$script:PublicIpsCache = $null
function Get-PublicIpsCached {
    if ($null -eq $script:PublicIpsCache) { $script:PublicIpsCache = Get-PublicIps }
    return $script:PublicIpsCache
}

function Write-UrlLine {
    param([string]$Label, [string]$Port, [string]$Path = '')
    Write-Host ('  {0,-8} : http://127.0.0.1:{1}{2}' -f $Label, $Port, $Path) -ForegroundColor Cyan
    $ips = Get-PublicIpsCached
    foreach ($ip in $ips) {
        if ($ip -match ':') {
            Write-Host ('             http://[{0}]:{1}{2}  (外网)' -f $ip, $Port, $Path) -ForegroundColor Cyan
        } else {
            Write-Host ('             http://{0}:{1}{2}  (外网)'   -f $ip, $Port, $Path) -ForegroundColor Cyan
        }
    }
}

function Do-Info {
    $port = '8080'; $token = '(未读取到)'; $ddir = $DataDir; $loglv = 'info'
    if (Use-Nssm) {
        $raw = & $NssmPath get $ServiceName AppEnvironmentExtra 2>$null
        foreach ($line in $raw) {
            if     ($line -match '^CFDM_HTTP_ADDR=(.*)$') { $port  = $Matches[1].TrimStart(':') }
            elseif ($line -match '^CFDM_API_TOKEN=(.*)$') { $token = $Matches[1] }
            elseif ($line -match '^CFDM_DATA_DIR=(.*)$')  { $ddir  = $Matches[1] }
            elseif ($line -match '^CFDM_LOG_LEVEL=(.*)$') { $loglv = $Matches[1] }
        }
    }
    $ver = '未知'
    if (Test-Path $BinPath) { $ver = ((& $BinPath version 2>$null) -join ' ') }
    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    $state = if ($svc) { "$($svc.Status)" } else { '未安装' }
    Write-Host 'cfdmgrd 运行信息'
    Write-Host '────────────────────────────────────────────'
    Write-Host ("  版本     : {0}" -f $ver)
    Write-Host ("  服务状态 : {0}" -f $state)
    Write-UrlLine '访问地址' "$port"
    Write-UrlLine 'API 文档' "$port" '/api/docs'
    if ((Get-PublicIpsCached).Count -gt 0) {
        Write-Host '  注: 外网地址能否实际访问取决于防火墙/安全组/NAT 是否放行该端口' -ForegroundColor Yellow
    }
    Write-Host ("  API 令牌 : {0}" -f $token)
    Write-Host ("  监听地址 : :{0}" -f $port)
    Write-Host ("  日志级别 : {0}" -f $loglv)
    Write-Host ("  程序路径 : {0}" -f $BinPath)
    Write-Host ("  管理命令 : {0}" -f (Join-Path $InstallDir 'cfm.cmd'))
    Write-Host ("  服务管理 : {0}" -f $NssmPath)
    Write-Host ("  数据目录 : {0}" -f $ddir)
    Write-Host ("  日志文件 : {0}" -f $LogFile)
    Write-Host ("  服务名称 : {0}  (services.msc)" -f $ServiceName)
    Write-CliPanel
}
function Do-Config {
    if (-not (Use-Nssm)) { Write-Host '[x] 未找到 nssm.exe, 无法读取服务配置' -ForegroundColor Red; exit 1 }
    $sub = if ($Rest.Count -ge 1) { $Rest[0] } else { 'show' }
    if ($sub -eq 'edit') { Need-Admin; & $NssmPath edit $ServiceName }
    else { & $NssmPath get $ServiceName AppEnvironmentExtra }
}
function Do-Version { & $BinPath version }
function Invoke-Installer([object[]]$extra) {
    Need-Admin
    $tmp = Join-Path $env:TEMP ("cfdmgr_install_" + [Guid]::NewGuid().ToString('N') + ".ps1")
    Invoke-WebRequest -Uri $RawUrl -OutFile $tmp -UseBasicParsing -Headers @{ 'User-Agent' = 'cfdmgrd-installer' }
    try { & powershell -NoProfile -ExecutionPolicy Bypass -File $tmp @extra }
    finally { Remove-Item -Force $tmp -ErrorAction SilentlyContinue }
}
function Show-Usage {
    Write-Host @"
cfm — cfdmgrd 管理命令

用法: cfm <命令> [参数]

服务管理:
  start            启动服务
  stop             停止服务
  restart          重启服务
  status           查看运行状态
  logs [-f]        查看日志 (加 -f 实时跟踪)
  enable           设置开机自启
  disable          取消开机自启

信息查看:
  info             显示完整运行信息 (地址/令牌/路径/状态) + 命令面板
  config [edit]    查看 (或 edit 用 NSSM 图形界面编辑) 服务配置
  version          显示版本信息

安装维护:
  install [参数]   重新安装 (参数透传给 install.ps1)
  update [参数]    更新到最新版 (保留端口/令牌/数据)
  uninstall        卸载

  help             显示本帮助
"@
}

function Write-CliTip {
    Write-Host '────────────────────────────────────────────'
    Write-Host '💡 输入 cfm 查看全部命令'
    Write-Host '────────────────────────────────────────────'
}

switch ($Cmd.ToLower()) {
    'start'     { Do-Start }
    'stop'      { Do-Stop }
    'restart'   { Do-Restart }
    'status'    { Do-Status }
    'logs'      { Do-Logs }
    'enable'    { Do-Enable }
    'disable'   { Do-Disable }
    'info'      { Do-Info; exit 0 }
    'config'    { Do-Config }
    'version'   { Do-Version }
    'update'    { Invoke-Installer (@('-Update') + $Rest) }
    'install'   { Invoke-Installer $Rest }
    'uninstall' {
        Invoke-Installer @('-Uninstall')
        Remove-Item -Force (Join-Path $InstallDir 'cfm.cmd'), (Join-Path $InstallDir 'cfm.ps1') -ErrorAction SilentlyContinue
        exit 0
    }
    default {
        Show-Usage
        if ($Cmd.ToLower() -in @('help', '-h', '--help', '-help')) { exit 0 } else { exit 2 }
    }
}

# 任意子命令执行完都补一行轻提示; help/uninstall 已 exit, logs -f 阻塞不会到这里
Write-CliTip
'@

    # cfm.ps1 含中文, 必须带 UTF-8 BOM, 否则 PowerShell 5.1 按 ANSI 解析会乱码/语法错
    $utf8Bom = New-Object System.Text.UTF8Encoding($true)
    [System.IO.File]::WriteAllText($cliPs1, ($head + "`r`n" + $body), $utf8Bom)
    # cfm.cmd 为纯 ASCII, 且 cmd.exe 不能带 BOM, 故用无 BOM 写入
    $cmdShim = "@echo off`r`npowershell -NoProfile -ExecutionPolicy Bypass -File `"%~dp0cfm.ps1`" %*`r`n"
    [System.IO.File]::WriteAllText($cliCmd, $cmdShim, (New-Object System.Text.UTF8Encoding($false)))

    # 确保安装目录在系统 PATH 中 (新开终端即可直接使用 cfm)
    $mp = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    if ($mp -notlike "*$InstallDir*") {
        [Environment]::SetEnvironmentVariable('Path', ($mp.TrimEnd(';') + ';' + $InstallDir), 'Machine')
        Write-Info "已将 $InstallDir 加入系统 PATH (新开终端生效)"
    }
    if (($env:Path -split ';') -notcontains $InstallDir) { $env:Path = $env:Path.TrimEnd(';') + ';' + $InstallDir }
    Write-Ok '管理命令已安装, 现在可使用: cfm <命令>'
}

# 从已注册服务读取监听端口 (用于更新后健康检查)
function Get-ServicePort {
    if (-not (Test-Service)) { return '' }
    $raw = & $script:NssmPath get $ServiceName AppEnvironmentExtra 2>$null
    $line = $raw | Where-Object { $_ -match '^CFDM_HTTP_ADDR=' } | Select-Object -First 1
    if ($line) { return ($line -split '=', 2)[1].TrimStart(':') }
    return ''
}

# 重启已有服务 (仅加载新二进制, 不改配置)
function Restart-CfdmgrService {
    if (Test-Service) {
        & $script:NssmPath restart $ServiceName | Out-Null
        Write-Ok '服务已重启'
    } else {
        Write-Warn '未发现已注册的服务, 跳过重启 (可重新安装以注册服务)'
    }
}

# ----------------------------------------------------------------------------
# 读取已安装二进制版本号 (如 1.2.14), 未安装则为空
# ----------------------------------------------------------------------------
function Get-InstalledVersion {
    if (Test-Path $script:BinPath) {
        $out = & $script:BinPath version 2>$null
        if ($out -match 'cfdmgrd\s+(\S+)') { return $Matches[1] }
    }
    return ''
}

# ----------------------------------------------------------------------------
# 健康检查
# ----------------------------------------------------------------------------
function Invoke-HealthCheck {
    Write-Info '等待服务就绪...'
    for ($i = 0; $i -lt 10; $i++) {
        & $script:BinPath health -addr "http://127.0.0.1:$($script:Port)" 2>$null | Out-Null
        if ($LASTEXITCODE -eq 0) {
            Write-Ok '服务健康检查通过 ✓'
            return
        }
        Start-Sleep -Seconds 1
    }
    Write-Warn '健康检查未通过 (服务可能仍在启动)。请稍后用 services.msc 查看服务状态与日志。'
}

# ----------------------------------------------------------------------------
# 安装总流程
# ----------------------------------------------------------------------------
function Invoke-Install {
    Write-Host '=== cfdmgrd 一键安装 (Windows) ===' -ForegroundColor White
    Get-Platform
    Resolve-Version
    Resolve-Port
    Resolve-Token
    Confirm-Install
    Install-Binary
    Install-Nssm
    Register-CfdmgrService
    Install-Cli
    Invoke-HealthCheck
    Write-Summary
}

# 打印 cfm 管理命令清单 (安装 / 更新结尾共用, 方便用户直接照着敲)
function Write-CliHint {
    Write-Host '────────────────────────────────────────────'
    Write-Host '  管理命令 (已加入 PATH, 新开终端任意目录可用):'
    # {0,-13} 定宽左对齐命令列 (最长 cfm uninstall = 13)，# 自然对齐
    $rows = @(
        @('cfm start',     '启动服务'),
        @('cfm stop',      '停止服务'),
        @('cfm restart',   '重启服务'),
        @('cfm status',    '查看状态'),
        @('cfm logs -f',   '实时日志'),
        @('cfm info',      '查看完整信息'),
        @('cfm config',    '查看/编辑配置'),
        @('cfm update',    '更新到最新版'),
        @('cfm uninstall', '卸载'),
        @('cfm help',      '查看全部命令')
    )
    foreach ($r in $rows) { Write-Host ('    {0,-13} # {1}' -f $r[0], $r[1]) }
    Write-Host '────────────────────────────────────────────'
}

# ----------------------------------------------------------------------------
# 外网 IP 探测 (多源混合, 每个超时 ~1.5s, 失败静默)
# ----------------------------------------------------------------------------
$PubIpV4Urls = @(
    'https://4.ipw.cn',
    'https://api.ip.sb/ip',
    'https://api.ipify.org',
    'https://ifconfig.me/ip',
    'https://ipv4.icanhazip.com',
    'http://members.3322.org/dyndns/getip'
)
$PubIpV6Urls = @('https://6.ipw.cn', 'https://ipv6.icanhazip.com')

function Get-PublicIps {
    $found = New-Object System.Collections.Generic.HashSet[string]
    foreach ($u in $PubIpV4Urls) {
        try {
            $r = Invoke-RestMethod -Uri $u -TimeoutSec 2 -UseBasicParsing -ErrorAction Stop
            if ($r) {
                $m = ([string]$r -replace '\s', '') | Select-String -Pattern '([0-9]{1,3}\.){3}[0-9]{1,3}' -AllMatches
                if ($m.Matches.Count -gt 0) { [void]$found.Add($m.Matches[0].Value) }
            }
        } catch { }
    }
    foreach ($u in $PubIpV6Urls) {
        try {
            $r = Invoke-RestMethod -Uri $u -TimeoutSec 2 -UseBasicParsing -ErrorAction Stop
            if ($r) {
                $s = ([string]$r -replace '\s', '')
                if ($s -match '^[0-9a-fA-F:]+$' -and $s -match ':') { [void]$found.Add($s) }
            }
        } catch { }
    }
    return @($found)
}

$script:PublicIpsCache = $null
function Get-PublicIpsCached {
    if ($null -eq $script:PublicIpsCache) { $script:PublicIpsCache = Get-PublicIps }
    return $script:PublicIpsCache
}

function Write-UrlLine {
    param([string]$Label, [string]$Port, [string]$Path = '')
    Write-Host ('  {0,-8} : http://127.0.0.1:{1}{2}' -f $Label, $Port, $Path) -ForegroundColor Cyan
    $ips = Get-PublicIpsCached
    foreach ($ip in $ips) {
        if ($ip -match ':') {
            Write-Host ('             http://[{0}]:{1}{2}  (外网)' -f $ip, $Port, $Path) -ForegroundColor Cyan
        } else {
            Write-Host ('             http://{0}:{1}{2}  (外网)'   -f $ip, $Port, $Path) -ForegroundColor Cyan
        }
    }
}

function Write-Summary {
    Write-Host ''
    Write-Host '✓ 安装完成!' -ForegroundColor Green
    Write-Host '────────────────────────────────────────────'
    Write-UrlLine '访问地址' "$($script:Port)"
    Write-UrlLine 'API 文档' "$($script:Port)" '/api/docs'
    if ((Get-PublicIpsCached).Count -gt 0) {
        Write-Host '  注: 外网地址能否实际访问取决于防火墙/安全组/NAT 是否放行该端口' -ForegroundColor Yellow
    }
    Write-Host ("  API 令牌 : {0}" -f $script:Token)
    Write-Host ("  安装目录 : {0}" -f $InstallDir)
    Write-Host ("  数据目录 : {0}" -f $DataDir)
    Write-Host ("  日志目录 : {0}" -f $LogDir)
    Write-CliHint
    Write-Warn '请妥善保存 API 令牌, 它是访问后台的唯一凭证!'
}

# ----------------------------------------------------------------------------
# 全自动更新流程 (保留现有端口/令牌/数据, 仅替换二进制并重启)
# ----------------------------------------------------------------------------
function Invoke-Update {
    Write-Host '=== cfdmgrd 全自动更新 (Windows) ===' -ForegroundColor White
    Get-Platform

    if (-not (Test-Path $script:BinPath)) {
        Die "未检测到已安装的 cfdmgrd ($($script:BinPath))。请先执行安装, 而非更新。"
    }

    $cur = Get-InstalledVersion
    if ($cur) { Write-Info "当前已安装版本: $cur" } else { Write-Info '当前已安装版本: 未知' }

    Resolve-Version
    $target = $script:Version.TrimStart('v')

    if ($cur -and $cur -eq $target -and -not $Force) {
        Write-Ok "已是最新版本 ($cur), 无需更新。"
        Write-Info '如需强制重装请加 -Force'
        return
    }

    Write-Info "准备更新: $(if ($cur) { $cur } else { '?' }) -> $target"
    # 先停服务再覆盖, 避免 exe 被占用
    if (Test-Service) { & $script:NssmPath stop $ServiceName 2>$null | Out-Null; Start-Sleep -Milliseconds 500 }
    Install-Binary
    Install-Cli                 # 顺带刷新管理命令 cfm 到最新
    Restart-CfdmgrService

    $script:Port = Get-ServicePort
    if ($script:Port) {
        Invoke-HealthCheck
    } else {
        Write-Warn '未能读取到现有端口, 跳过健康检查 (服务应已重启)'
    }

    Write-Host ''
    Write-Host "✓ 更新完成! 版本: $target" -ForegroundColor Green
    if ($script:Port) {
        # 重置缓存, 让 cfm update 也能拿到最新外网 IP
        $script:PublicIpsCache = $null
        Write-UrlLine '访问地址' "$($script:Port)"
        if ((Get-PublicIpsCached).Count -gt 0) {
            Write-Host '  注: 外网地址能否实际访问取决于防火墙/安全组/NAT 是否放行该端口' -ForegroundColor Yellow
        }
    }
    Write-Info '现有端口、API 令牌与数据均未改动。'
    Write-CliHint
}

# ----------------------------------------------------------------------------
# 卸载流程
# ----------------------------------------------------------------------------
function Invoke-Uninstall {
    Write-Host '=== cfdmgrd 卸载 (Windows) ===' -ForegroundColor White

    if (Test-Path $script:NssmPath) {
        if (Test-Service) {
            & $script:NssmPath stop $ServiceName 2>$null | Out-Null
            & $script:NssmPath remove $ServiceName confirm 2>$null | Out-Null
            Write-Ok '已移除 Windows 服务'
        } else {
            Write-Info '未发现已注册服务, 跳过'
        }
    } elseif (Test-Service) {
        # 没有 nssm.exe 时退而用 sc.exe 删除
        & sc.exe stop $ServiceName 2>$null | Out-Null
        & sc.exe delete $ServiceName 2>$null | Out-Null
        Write-Ok '已移除 Windows 服务'
    }

    if (Test-Path $script:BinPath) {
        Remove-Item -Force $script:BinPath -ErrorAction SilentlyContinue
        Write-Ok "已删除二进制 $($script:BinPath)"
    }

    # 删除管理命令 cfm (cfm.cmd + cfm.ps1)
    Remove-Item -Force (Join-Path $InstallDir 'cfm.cmd'), (Join-Path $InstallDir 'cfm.ps1') -ErrorAction SilentlyContinue

    $r = Read-Prompt "是否同时删除配置与数据目录 ($(Split-Path $DataDir -Parent))? [y/N]" 'N'
    if ($r -match '^(y|yes)$') {
        Remove-Item -Recurse -Force (Split-Path $DataDir -Parent) -ErrorAction SilentlyContinue
        Remove-Item -Recurse -Force $InstallDir -ErrorAction SilentlyContinue
        Write-Ok '已删除配置与数据'
    } else {
        Write-Info "保留数据目录 $DataDir"
        # 仅清理已无用的 nssm.exe 与空安装目录
        Remove-Item -Force $script:NssmPath -ErrorAction SilentlyContinue
        if ((Test-Path $InstallDir) -and -not (Get-ChildItem $InstallDir -ErrorAction SilentlyContinue)) {
            Remove-Item -Force $InstallDir -ErrorAction SilentlyContinue
        }
    }
    Write-Ok '卸载完成'
}

# ----------------------------------------------------------------------------
# 入口
# ----------------------------------------------------------------------------
function Main {
    if ($Help) { Show-Usage; return }

    # 控制台 UTF-8, 避免中文输出乱码
    try { [Console]::OutputEncoding = [Text.Encoding]::UTF8 } catch { }

    Assert-Admin
    Initialize-Net

    try {
        if ($Uninstall)  { Invoke-Uninstall }
        elseif ($Update) { Invoke-Update }
        else             { Invoke-Install }
    } finally {
        if ($script:OldProgress) { $global:ProgressPreference = $script:OldProgress }
        Cleanup
    }
}

Main
