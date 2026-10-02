# Реиндексация базы данных
Write-Host "=== Запуск реиндексации ===" -ForegroundColor Cyan

try {
    $response = Invoke-RestMethod -Uri "http://localhost:3000/api/admin/reindex" -Method Post -ContentType "application/json" -TimeoutSec 300
    Write-Host "✅ Реиндексация завершена успешно!" -ForegroundColor Green
    Write-Host $response
} catch {
    Write-Host "❌ Ошибка реиндексации: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host $_.Exception
}

Write-Host "`n=== Проверка количества фрагментов ===" -ForegroundColor Cyan
try {
    $sources = Invoke-RestMethod -Uri "http://localhost:3000/api/admin/sources" -Method Get
    Write-Host "Источников в базе: $($sources.Count)" -ForegroundColor Yellow
    $sources | ForEach-Object { Write-Host "  - $($_.title): $($_.url)" }
} catch {
    Write-Host "⚠️ Не удалось получить список источников" -ForegroundColor Yellow
}
