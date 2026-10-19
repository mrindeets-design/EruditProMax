# Скрипт для запуска сервера Erudit
# Использование: .\start_server.ps1

$ErrorActionPreference = "Stop"
$ServerPath = "$PSScriptRoot\erudit.exe"
$ServerPort = 3000

Write-Host ""
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  🚀 ЗАПУСК СЕРВЕРА ERUDIT" -ForegroundColor Green
Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""

# Проверка наличия бинарника
if (-not (Test-Path $ServerPath)) {
    Write-Host "❌ Файл erudit.exe не найден!" -ForegroundColor Red
    Write-Host "📝 Выполните сборку: go build -o erudit.exe" -ForegroundColor Yellow
    exit 1
}

# Остановка существующих процессов
Write-Host "🔍 Проверка запущенных процессов..." -ForegroundColor Yellow
$existingProcesses = Get-Process -Name "erudit" -ErrorAction SilentlyContinue
if ($existingProcesses) {
    Write-Host "⚠️  Найдено $($existingProcesses.Count) запущенных процессов" -ForegroundColor Yellow
    Write-Host "🛑 Остановка старых процессов..." -ForegroundColor Yellow
    $existingProcesses | Stop-Process -Force
    Start-Sleep -Seconds 2
    Write-Host "✅ Старые процессы остановлены" -ForegroundColor Green
} else {
    Write-Host "✅ Нет запущенных процессов" -ForegroundColor Green
}

# Проверка занятости порта
Write-Host ""
Write-Host "🔍 Проверка порта $ServerPort..." -ForegroundColor Yellow
$portInUse = Get-NetTCPConnection -LocalPort $ServerPort -ErrorAction SilentlyContinue
if ($portInUse) {
    Write-Host "⚠️  Порт $ServerPort занят процессом $($portInUse.OwningProcess)" -ForegroundColor Yellow
    Write-Host "🛑 Освобождение порта..." -ForegroundColor Yellow
    Stop-Process -Id $portInUse.OwningProcess -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
    Write-Host "✅ Порт освобожден" -ForegroundColor Green
} else {
    Write-Host "✅ Порт свободен" -ForegroundColor Green
}

# Запуск сервера
Write-Host ""
Write-Host "🚀 Запуск сервера..." -ForegroundColor Yellow
$process = Start-Process -FilePath $ServerPath -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 3

# Проверка запуска
if (Get-Process -Id $process.Id -ErrorAction SilentlyContinue) {
    Write-Host "✅ Сервер запущен (PID: $($process.Id))" -ForegroundColor Green
    
    # Проверка доступности API
    Write-Host "🔍 Проверка доступности API..." -ForegroundColor Yellow
    Start-Sleep -Seconds 5
    
    try {
        $response = Invoke-WebRequest -Uri "http://localhost:$ServerPort/api/health" -Method GET -TimeoutSec 5 -ErrorAction Stop
        Write-Host "✅ API доступен" -ForegroundColor Green
    } catch {
        Write-Host "⚠️  API пока недоступен (это нормально при первом запуске)" -ForegroundColor Yellow
    }
    
    Write-Host ""
    Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
    Write-Host "  ✅ СЕРВЕР УСПЕШНО ЗАПУЩЕН" -ForegroundColor Green
    Write-Host "═══════════════════════════════════════════════════════════" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "📍 URL: http://localhost:$ServerPort" -ForegroundColor White
    Write-Host "📍 PID: $($process.Id)" -ForegroundColor White
    Write-Host ""
    Write-Host "Для остановки используйте: .\stop_server.ps1" -ForegroundColor Yellow
    Write-Host ""
} else {
    Write-Host "❌ Не удалось запустить сервер" -ForegroundColor Red
    exit 1
}
