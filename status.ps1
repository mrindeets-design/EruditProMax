# Скрипт для проверки статуса сервера Erudit
# Использование: .\status.ps1

$ErrorActionPreference = "Continue"
$ServerPort = 3000

Write-Host ""
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  📊 СТАТУС СЕРВЕРА ERUDIT" -ForegroundColor Cyan
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""

# Проверка процессов
Write-Host "🔍 Процессы:" -ForegroundColor Yellow
$processes = Get-Process -Name "erudit" -ErrorAction SilentlyContinue
if ($processes) {
    Write-Host "   ✅ Запущено процессов: $($processes.Count)" -ForegroundColor Green
    foreach ($proc in $processes) {
        $uptime = (Get-Date) - $proc.StartTime
        Write-Host "   - PID: $($proc.Id)" -ForegroundColor White
        Write-Host "     Запущен: $($proc.StartTime.ToString('yyyy-MM-dd HH:mm:ss'))" -ForegroundColor Gray
        Write-Host "     Uptime: $([math]::Floor($uptime.TotalHours))ч $($uptime.Minutes)м" -ForegroundColor Gray
        Write-Host "     Memory: $([math]::Round($proc.WorkingSet64 / 1MB, 2)) MB" -ForegroundColor Gray
    }
} else {
    Write-Host "   ❌ Нет запущенных процессов" -ForegroundColor Red
}

# Проверка порта
Write-Host ""
Write-Host "🔍 Порт $ServerPort`:" -ForegroundColor Yellow
$portInUse = Get-NetTCPConnection -LocalPort $ServerPort -State Listen -ErrorAction SilentlyContinue
if ($portInUse) {
    Write-Host "   ✅ Порт прослушивается (PID: $($portInUse.OwningProcess))" -ForegroundColor Green
} else {
    Write-Host "   ❌ Порт не прослушивается" -ForegroundColor Red
}

# Проверка API
Write-Host ""
Write-Host "🔍 API:" -ForegroundColor Yellow
try {
    $response = Invoke-WebRequest -Uri "http://localhost:$ServerPort/api/health" -Method GET -TimeoutSec 3 -ErrorAction Stop
    Write-Host "   ✅ API доступен (HTTP $($response.StatusCode))" -ForegroundColor Green
} catch {
    Write-Host "   ❌ API недоступен" -ForegroundColor Red
    if ($_.Exception.Message) {
        Write-Host "      Ошибка: $($_.Exception.Message)" -ForegroundColor Gray
    }
}

# Проверка бинарника
Write-Host ""
Write-Host "🔍 Бинарник:" -ForegroundColor Yellow
if (Test-Path "$PSScriptRoot\erudit.exe") {
    $fileInfo = Get-Item "$PSScriptRoot\erudit.exe"
    Write-Host "   ✅ Файл существует" -ForegroundColor Green
    Write-Host "      Размер: $([math]::Round($fileInfo.Length / 1MB, 2)) MB" -ForegroundColor Gray
    Write-Host "      Дата: $($fileInfo.LastWriteTime)" -ForegroundColor Gray
} else {
    Write-Host "   ❌ Файл не найден" -ForegroundColor Red
}

# Проверка fallback данных
Write-Host ""
Write-Host "🔍 Fallback данные:" -ForegroundColor Yellow
if (Test-Path "$PSScriptRoot\fallback_data.json") {
    $fileInfo = Get-Item "$PSScriptRoot\fallback_data.json"
    Write-Host "   ✅ Файл существует" -ForegroundColor Green
    Write-Host "      Размер: $([math]::Round($fileInfo.Length / 1KB, 2)) KB" -ForegroundColor Gray
    Write-Host "      Дата: $($fileInfo.LastWriteTime)" -ForegroundColor Gray
} else {
    Write-Host "   ❌ Файл не найден" -ForegroundColor Red
}

Write-Host ""
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan

# Итоговый статус
Write-Host ""
if ($processes -and $portInUse) {
    Write-Host "  ✅ СЕРВЕР РАБОТАЕТ" -ForegroundColor Green
} else {
    Write-Host "  ❌ СЕРВЕР НЕ РАБОТАЕТ" -ForegroundColor Red
    Write-Host ""
    Write-Host "Для запуска используйте: .\start_server.ps1" -ForegroundColor Yellow
}

Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""
