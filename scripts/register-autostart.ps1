$scriptPath = "C:\Users\fbvic\Desktop\Все данные\FBDG Проекты\Fahriddin AI\scripts\dev-up.ps1"
$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$scriptPath`""

$triggerLogon = New-ScheduledTaskTrigger -AtLogOn
$triggerRepeat = New-ScheduledTaskTrigger -Once -At (Get-Date) -RepetitionInterval (New-TimeSpan -Minutes 15) -RepetitionDuration (New-TimeSpan -Days 3650)

$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -MultipleInstances IgnoreNew -ExecutionTimeLimit (New-TimeSpan -Minutes 25)

Register-ScheduledTask -TaskName "FahriddinAI-DevStack" `
    -Action $action `
    -Trigger @($triggerLogon, $triggerRepeat) `
    -Settings $settings `
    -Description "Keeps the Fahriddin AI local dev stack (Postgres, cloudflared tunnel, Go agent) up: starts at logon and self-heals every 15 min until migrated to VDS." `
    -Force

Write-Host ""
Write-Host "Registered. Verifying:" -ForegroundColor Cyan
Get-ScheduledTask -TaskName "FahriddinAI-DevStack" | Select-Object TaskName, State
