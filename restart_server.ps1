# Скрипт для перезапуска сервера Erudit
# Использование: .\restart_server.ps1

$ErrorActionPreference = "Stop"

Write-Host ""
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  🔄 ПЕРЕЗАПУСК СЕРВЕРА ERUDIT" -ForegroundColor Cyan
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""

# Остановка
Write-Host "🛑 Остановка сервера..." -ForegroundColor Yellow
& "$PSScriptRoot\stop_server.ps1"

Start-Sleep -Seconds 2

# Запуск
Write-Host "🚀 Запуск сервера..." -ForegroundColor Yellow
& "$PSScriptRoot\start_server.ps1"
