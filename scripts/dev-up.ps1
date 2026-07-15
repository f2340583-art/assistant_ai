#requires -version 5.1
<#
  Brings up the full local dev stack (Postgres, cloudflared tunnel, Go agent)
  as processes detached from this script's own session, so they survive the
  invoking shell/session being torn down. Idempotent - safe to re-run; only
  restarts whatever isn't already healthy.
#>

$ErrorActionPreference = "Stop"

$ProjectDir = Split-Path -Parent $PSScriptRoot
$PgBin      = "C:\Users\fbvic\pgsql16\pgsql\bin"
$PgData     = "C:\Users\fbvic\pgsql16\data"
# Moved off 5433 on 2026-07-14: orphaned postgres.exe workers from an
# earlier crash got stuck holding it in a broken state (invisible to
# netstat, immune to Stop-Process/taskkill). 5434 sidesteps them.
$PgPort     = 5434
$PgLog      = "C:\Users\fbvic\pgsql16\logfile.log"
$LogDir     = Join-Path $ProjectDir "scripts\logs"
$EnvFile    = Join-Path $ProjectDir ".env"

New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

function Test-Port($portNum) {
    try {
        $client = New-Object System.Net.Sockets.TcpClient
        $task = $client.ConnectAsync("127.0.0.1", $portNum)
        $ok = $task.Wait(1500)
        $client.Close()
        return $ok
    } catch { return $false }
}

function Get-EnvValue($key) {
    $line = Get-Content $EnvFile | Where-Object { $_ -match "^$key=" } | Select-Object -First 1
    if ($line) { return ($line -split "=", 2)[1].Trim() }
    return $null
}

function Set-EnvValue($key, $value) {
    $content = Get-Content $EnvFile
    $found = $false
    $content = $content | ForEach-Object {
        if ($_ -match "^$key=") { $found = $true; "$key=$value" } else { $_ }
    }
    if (-not $found) { $content += "$key=$value" }
    Set-Content -Path $EnvFile -Value $content
}

Write-Host "=== 1. Postgres (port $PgPort) ===" -ForegroundColor Cyan
if (Test-Port $PgPort) {
    Write-Host "already up" -ForegroundColor Green
} else {
    Write-Host "starting..."
    & "$PgBin\pg_ctl.exe" -D $PgData -o "-p $PgPort" -l $PgLog start | Out-Null
    $ok = $false
    for ($i = 0; $i -lt 20; $i++) {
        Start-Sleep -Seconds 1
        if (Test-Port $PgPort) { $ok = $true; break }
    }
    if ($ok) { Write-Host "started" -ForegroundColor Green }
    else { Write-Host "FAILED to start - check $PgLog" -ForegroundColor Red; exit 1 }
}

Write-Host "=== 2. Cloudflare tunnel ===" -ForegroundColor Cyan
$currentUrl = Get-EnvValue "WEBAPP_URL"
$tunnelHealthy = $false
if ($currentUrl) {
    try {
        $resp = Invoke-WebRequest -Uri $currentUrl -UseBasicParsing -TimeoutSec 5
        if ($resp.StatusCode -eq 200) { $tunnelHealthy = $true }
    } catch { $tunnelHealthy = $false }
}

if ($tunnelHealthy) {
    Write-Host "already up: $currentUrl" -ForegroundColor Green
} else {
    Write-Host "current tunnel dead, restarting..."
    Get-Process cloudflared -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1

    $cfLog = Join-Path $LogDir "cloudflared.log"
    Remove-Item $cfLog -ErrorAction SilentlyContinue
    Start-Process -FilePath "cloudflared" `
        -ArgumentList "tunnel","--url","http://localhost:8080" `
        -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $LogDir "cloudflared.out.log") `
        -RedirectStandardError $cfLog

    $newUrl = $null
    for ($i = 0; $i -lt 20; $i++) {
        Start-Sleep -Seconds 1
        $match = Select-String -Path $cfLog -Pattern "https://[a-z0-9-]+\.trycloudflare\.com" -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($match) {
            $newUrl = $match.Matches[0].Value
            break
        }
    }

    if ($newUrl) {
        Set-EnvValue "WEBAPP_URL" $newUrl
        Write-Host "started: $newUrl" -ForegroundColor Green
    } else {
        Write-Host "FAILED to get tunnel URL - check $cfLog" -ForegroundColor Red
        exit 1
    }
}

Write-Host "=== 3. Go agent (port 8080) ===" -ForegroundColor Cyan
$agentHealthy = Test-Port 8080
$envChanged = -not $tunnelHealthy
if ($agentHealthy -and -not $envChanged) {
    Write-Host "already up" -ForegroundColor Green
} else {
    Write-Host "starting (fresh env, so restarting even if already running)..."
    Get-CimInstance Win32_Process -Filter "Name='agent.exe' OR (Name='go.exe' AND CommandLine LIKE '%cmd/agent%')" |
        ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
    Start-Sleep -Seconds 1

    $agentLog = Join-Path $LogDir "agent.log"
    Start-Process -FilePath "go" `
        -ArgumentList "run","./cmd/agent" `
        -WorkingDirectory $ProjectDir `
        -WindowStyle Hidden `
        -RedirectStandardOutput $agentLog `
        -RedirectStandardError (Join-Path $LogDir "agent.err.log")

    $ok = $false
    for ($i = 0; $i -lt 30; $i++) {
        Start-Sleep -Seconds 1
        if (Test-Port 8080) { $ok = $true; break }
    }
    if ($ok) { Write-Host "started" -ForegroundColor Green }
    else { Write-Host "FAILED to start - check $agentLog" -ForegroundColor Red; exit 1 }
}

Write-Host ""
Write-Host "=== Status ===" -ForegroundColor Cyan
Write-Host "Local:  http://localhost:8080"
Write-Host "Public: $(Get-EnvValue 'WEBAPP_URL')"
Write-Host "Logs:   $LogDir"
