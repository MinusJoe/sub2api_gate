# 端到端验证：确认网关出站 TLS ClientHello 与官方 Codex 基线一致。
#
# 用法（Windows PowerShell / cmd）：
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify-openai-tls-fp.ps1
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify-openai-tls-fp.ps1 -Capture out.jsonl   # 只判定已有抓包
#
# 步骤：起 fpcap peek → 把「一个测试账号」的代理指向它并打开 enable_tls_fingerprint →
#       用该账号发一次请求 → 回车 → 脚本比对基线并给出 PASS/FAIL。
param(
    [string]$Listen = "127.0.0.1:18443",
    [string]$Upstream = "chatgpt.com:443",
    [string]$Out = "",
    [string]$Baseline = "internal/pkg/tlsfingerprint/testdata/codex_cli_rs_0.169.0_linux.jsonl",
    [string]$Fpcap = "",
    [string]$Capture = ""
)

$ErrorActionPreference = "Stop"
$ExpectedMd5 = "0b85eb0d4981e69064e40753e4f0ac5f"

function Resolve-Fpcap([string]$explicit) {
    if ($explicit) { return $explicit }
    foreach ($candidate in @(".\fpcap.exe", ".\fpcap", "fpcap.exe", "fpcap")) {
        $cmd = Get-Command $candidate -ErrorAction SilentlyContinue
        if ($cmd) { return $cmd.Source }
    }
    return ""
}

function Test-ListenFree([string]$listen) {
    $parts = $listen.Split(":")
    $port = [int]$parts[-1]
    $listener = $null
    try {
        $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $port)
        $listener.Start()
        return $true
    } catch {
        return $false
    } finally {
        if ($listener) { $listener.Stop() }
    }
}

function Get-FreeListen([string]$listen) {
    if (Test-ListenFree $listen) { return $listen }
    $probe = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $probe.Start()
    $port = ([System.Net.IPEndPoint]$probe.LocalEndpoint).Port
    $probe.Stop()
    $hostPart = $listen.Split(":")[0]
    return "${hostPart}:$port"
}

function Get-CaptureStats([string]$path) {
    $lines = Get-Content -LiteralPath $path | Where-Object { $_.Trim() -ne "" }
    $total = 0
    $md5s = @{}
    $noAlpn = 0
    foreach ($line in $lines) {
        try { $rec = $line | ConvertFrom-Json } catch { continue }
        if (-not $rec.tls) { continue }
        $total++
        $md5 = [string]$rec.tls.ja3_md5
        if ($md5) { $md5s[$md5] = 1 + ($md5s[$md5] -as [int]) }
        if ($null -ne $rec.tls.alpn -and @($rec.tls.alpn).Count -eq 0) { $noAlpn++ }
    }
    $dominant = ""
    $dominantCount = 0
    foreach ($k in $md5s.Keys) {
        if ($md5s[$k] -gt $dominantCount) { $dominant = $k; $dominantCount = $md5s[$k] }
    }
    return [pscustomobject]@{
        Total         = $total
        DominantMd5   = $dominant
        DominantCount = $dominantCount
        NoAlpn        = $noAlpn
    }
}

# ---- 采集 ----
if (-not $Capture) {
    if (-not $Out) { $Out = Join-Path $env:TEMP ("gateway-tls-" + (Get-Date -Format "yyyyMMdd-HHmmss") + ".jsonl") }
    $fpcapPath = Resolve-Fpcap $Fpcap
    if (-not $fpcapPath) {
        Write-Host "找不到 fpcap。先在 backend/ 下运行: go build -o fpcap.exe ./cmd/fpcap" -ForegroundColor Red
        exit 2
    }
    if (-not (Test-Path -LiteralPath $Baseline)) {
        Write-Host "找不到基线文件: $Baseline" -ForegroundColor Red
        exit 2
    }
    Remove-Item -LiteralPath $Out -ErrorAction SilentlyContinue

    $effectiveListen = Get-FreeListen $Listen
    if ($effectiveListen -ne $Listen) {
        Write-Host "端口 $Listen 已被占用，改用 $effectiveListen" -ForegroundColor Yellow
        $Listen = $effectiveListen
    }

    $proc = Start-Process -FilePath $fpcapPath -ArgumentList @(
        "peek", "-listen", $Listen, "-upstream", $Upstream, "-out", $Out
    ) -PassThru -NoNewWindow
    Start-Sleep -Milliseconds 400
    if ($proc.HasExited) {
        Write-Host "fpcap 启动失败（退出码 $($proc.ExitCode)）。常见原因：端口被占用、upstream 参数不合法。" -ForegroundColor Red
        exit 2
    }

    Write-Host ""
    Write-Host "=== 采集已启动 ===" -ForegroundColor Cyan
    Write-Host "监听:  $Listen  ->  $Upstream"
    Write-Host "输出:  $Out"
    Write-Host ""
    Write-Host "请执行："
    Write-Host "  1) 选一个测试账号，把代理设置为: http://$Listen"
    Write-Host "  2) 该账号 extra 打开:           `"enable_tls_fingerprint`": true"
    Write-Host "  3) 不要绑定 tls_fingerprint_profile_id"
    Write-Host "  4) 用该账号发一次请求（HTTP 与 WS 各一次更好）"
    Write-Host ""
    Read-Host "完成后按回车，脚本会停止采集并判定" | Out-Null

    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 300
    $Capture = $Out
}

if (-not (Test-Path -LiteralPath $Capture)) {
    Write-Host "FAIL: 抓包文件不存在: $Capture" -ForegroundColor Red
    exit 1
}

$stats = Get-CaptureStats $Capture
if ($stats.Total -eq 0) {
    Write-Host "FAIL: 没抓到任何 TLS 连接。检查账号代理是否指向 $Listen。" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "=== 采集结果 ===" -ForegroundColor Cyan
Write-Host ("连接数:        {0}" -f $stats.Total)
Write-Host ("主导 JA3 MD5:  {0}（{1} 条）" -f $stats.DominantMd5, $stats.DominantCount)
Write-Host ("无 ALPN 连接:  {0} / {1}" -f $stats.NoAlpn, $stats.Total)
Write-Host ("期望 MD5:      {0}" -f $ExpectedMd5)

if ($stats.DominantMd5 -ne $ExpectedMd5) {
    Write-Host ""
    Write-Host "FAIL: 主导指纹与官方不一致。" -ForegroundColor Red
    Write-Host "排查顺序："
    Write-Host "  1) 账号代理是 https:// 或未知 scheme → 指纹静默降级为 Go TLS"
    Write-Host "  2) 有 OpenAI OAuth 插件接管 → 看日志 service.openai_tlsfp 的告警"
    Write-Host "  3) enable_tls_fingerprint 未生效"
    Write-Host "  4) 账号绑定了 tls_fingerprint_profile_id（会改用 DB 里的 Chrome 模板）"
    exit 1
}

if ($stats.NoAlpn -ne $stats.Total) {
    $withAlpn = $stats.Total - $stats.NoAlpn
    Write-Host ("WARN: 有 {0} 条连接带了 ALPN；走代理的官方客户端不应出现。" -f $withAlpn) -ForegroundColor Yellow
}

Write-Host ""
Write-Host ("PASS: 网关出站指纹与官方 Linux 客户端一致（JA3 MD5 = {0}，无 ALPN）。" -f $ExpectedMd5) -ForegroundColor Green
exit 0
