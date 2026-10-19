# Скрипт для пересборки и перезапуска сервера Erudit
# Использование: .\rebuild.ps1

$ErrorActionPreference = "Stop"

Write-Host ""
Write-Host "===================================================================" -ForegroundColor Cyan
Write-Host "  REBUILD AND RESTART ERUDIT SERVER" -ForegroundColor Cyan
Write-Host "===================================================================" -ForegroundColor Cyan
Write-Host ""

# Остановка сервера
Write-Host "Stopping server..." -ForegroundColor Yellow
$processes = Get-Process -Name "erudit" -ErrorAction SilentlyContinue
if ($processes) {
    $processes | Stop-Process -Force
    Start-Sleep -Seconds 2
    Write-Host "Server stopped" -ForegroundColor Green
}

# Удаление старого бинарника
Write-Host ""
Write-Host "Removing old binary..." -ForegroundColor Yellow
if (Test-Path "$PSScriptRoot\erudit.exe") {
    Remove-Item "$PSScriptRoot\erudit.exe" -Force
    Write-Host "Old binary removed" -ForegroundColor Green
}

# Сборка
Write-Host ""
Write-Host "Building new binary..." -ForegroundColor Yellow
$env:CGO_ENABLED = "0"
go build -o "$PSScriptRoot\erudit.exe"

if ($LASTEXITCODE -ne 0) {
    Write-Host "Build failed" -ForegroundColor Red
    exit 1
}

Write-Host "Build completed" -ForegroundColor Green

# Проверка бинарника
if (-not (Test-Path "$PSScriptRoot\erudit.exe")) {
    Write-Host "Binary not created" -ForegroundColor Red
    exit 1
}

$fileInfo = Get-Item "$PSScriptRoot\erudit.exe"
Write-Host "Size: $([math]::Round($fileInfo.Length / 1MB, 2)) MB" -ForegroundColor Gray
Write-Host "Date: $($fileInfo.LastWriteTime)" -ForegroundColor Gray

# Запуск
Write-Host ""
Write-Host "Starting new server..." -ForegroundColor Yellow

$ServerPath = "$PSScriptRoot\erudit.exe"
$process = Start-Process -FilePath $ServerPath -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 8

if (Get-Process -Id $process.Id -ErrorAction SilentlyContinue) {
    Write-Host ""
    Write-Host "===================================================================" -ForegroundColor Cyan
    Write-Host "  SERVER STARTED SUCCESSFULLY" -ForegroundColor Green
    Write-Host "===================================================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "URL: http://localhost:3000" -ForegroundColor White
    Write-Host "PID: $($process.Id)" -ForegroundColor White
    Write-Host ""
} else {
    Write-Host "Failed to start server" -ForegroundColor Red
    exit 1
}

