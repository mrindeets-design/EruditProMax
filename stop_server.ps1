# Скрипт для остановки сервера Erudit
# Использование: .\stop_server.ps1

$ErrorActionPreference = "Stop"

Write-Host ""
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  🛑 ОСТАНОВКА СЕРВЕРА ERUDIT" -ForegroundColor Yellow
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""

# Поиск процессов
Write-Host "🔍 Поиск запущенных процессов..." -ForegroundColor Yellow
$processes = Get-Process -Name "erudit" -ErrorAction SilentlyContinue

if ($processes) {
    Write-Host "⚠️  Найдено процессов: $($processes.Count)" -ForegroundColor Yellow
    
    foreach ($proc in $processes) {
        Write-Host "   - PID: $($proc.Id), Start: $($proc.StartTime)" -ForegroundColor Gray
    }
    
    Write-Host ""
    Write-Host "🛑 Остановка процессов..." -ForegroundColor Yellow
    $processes | Stop-Process -Force
    Start-Sleep -Seconds 2
    
    # Проверка
    $remainingProcesses = Get-Process -Name "erudit" -ErrorAction SilentlyContinue
    if ($remainingProcesses) {
        Write-Host "⚠️  Не все процессы остановлены, повторная попытка..." -ForegroundColor Yellow
        $remainingProcesses | Stop-Process -Force
        Start-Sleep -Seconds 1
    }
    
    Write-Host "✅ Все процессы остановлены" -ForegroundColor Green
} else {
    Write-Host "✅ Нет запущенных процессов" -ForegroundColor Green
}

Write-Host ""
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  ✅ СЕРВЕР ОСТАНОВЛЕН" -ForegroundColor Green
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""
