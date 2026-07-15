#requires -version 5.1
<#
  Cleanly stops the local dev stack (Go agent, cloudflared, Postgres).
  Use this instead of killing processes ad hoc, so Postgres shuts down
  gracefully (WAL checkpoint) rather than crashing.
#>

$PgBin  = "C:\Users\fbvic\pgsql16\pgsql\bin"
$PgData = "C:\Users\fbvic\pgsql16\data"

Write-Host "Stopping Go agent..." -ForegroundColor Cyan
Get-CimInstance Win32_Process -Filter "Name='agent.exe' OR (Name='go.exe' AND CommandLine LIKE '%cmd/agent%')" |
    ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }

Write-Host "Stopping cloudflared..." -ForegroundColor Cyan
Get-Process cloudflared -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue

Write-Host "Stopping Postgres (graceful)..." -ForegroundColor Cyan
& "$PgBin\pg_ctl.exe" -D $PgData stop -m fast 2>&1 | Out-Null

Write-Host "Done." -ForegroundColor Green
