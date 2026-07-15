#requires -version 5.1
<# Quick health check of the local dev stack. Read-only - fixes nothing. #>

function Test-Port($portNum) {
    try {
        $client = New-Object System.Net.Sockets.TcpClient
        $ok = $client.ConnectAsync("127.0.0.1", $portNum).Wait(1500)
        $client.Close()
        return $ok
    } catch { return $false }
}

$EnvFile = Join-Path (Split-Path -Parent $PSScriptRoot) ".env"
$webappUrl = (Get-Content $EnvFile | Where-Object { $_ -match "^WEBAPP_URL=" } | Select-Object -First 1) -replace "^WEBAPP_URL=", ""

Write-Host "Postgres (5434): " -NoNewline
if (Test-Port 5434) { Write-Host "UP" -ForegroundColor Green } else { Write-Host "DOWN" -ForegroundColor Red }

Write-Host "Agent    (8080): " -NoNewline
if (Test-Port 8080) { Write-Host "UP" -ForegroundColor Green } else { Write-Host "DOWN" -ForegroundColor Red }

Write-Host "Tunnel   ($webappUrl): " -NoNewline
try {
    $r = Invoke-WebRequest -Uri $webappUrl -UseBasicParsing -TimeoutSec 5
    if ($r.StatusCode -eq 200) { Write-Host "UP" -ForegroundColor Green } else { Write-Host "DOWN (status $($r.StatusCode))" -ForegroundColor Red }
} catch { Write-Host "DOWN" -ForegroundColor Red }
