# Тестирование контекстного поиска
# Проверяем, что бот теперь использует контекст при поиске фрагментов

Write-Host "=== ТЕСТ: Контекстный поиск ===" -ForegroundColor Cyan
Write-Host ""

$sessionId = "test-context-search-$(Get-Date -Format 'HHmmss')"
$baseUrl = "http://localhost:3000"

function Send-Message {
    param($message)
    
    Write-Host "→ Пользователь: $message" -ForegroundColor Yellow
    
    $body = @{
        message = $message
        session_id = $sessionId
    } | ConvertTo-Json
    
    $response = Invoke-RestMethod -Uri "$baseUrl/api/chat" -Method Post -Body $body -ContentType "application/json; charset=utf-8"
    
    # Выводим полный ответ для дебага
    Write-Host "← Бот: $($response.reply)" -ForegroundColor Green
    Write-Host "   Sources: $($response.sources.Count)" -ForegroundColor Gray
    
    if ($response.sources.Count -gt 0) {
        Write-Host "   Top source: $($response.sources[0].title)" -ForegroundColor Gray
    }
    
    Write-Host ""
    
    return $response
}

# Тест 1: Первый вопрос о стоимости (без указания специальности)
Write-Host "--- Тест 1: Первый вопрос ---" -ForegroundColor Magenta
$response1 = Send-Message "Привет, подскажи стоимость обучения"

Start-Sleep -Seconds 3

# Тест 2: Уточнение "дизайн" - должно найти фрагменты благодаря контексту
Write-Host "--- Тест 2: Уточнение с использованием контекста ---" -ForegroundColor Magenta
$response2 = Send-Message "дизайн"

# Проверка результатов
Write-Host ""
Write-Host "=== РЕЗУЛЬТАТЫ ===" -ForegroundColor Cyan

if ($response2.sources.Count -gt 0) {
    Write-Host "✅ SUCCESS: Бот нашел $($response2.sources.Count) источников для уточнения 'дизайн'" -ForegroundColor Green
    Write-Host "   Это означает, что контекст диалога используется в поиске!" -ForegroundColor Green
} else {
    Write-Host "❌ FAIL: Бот не нашел источников для уточнения 'дизайн'" -ForegroundColor Red
    Write-Host "   Контекст не используется в поиске" -ForegroundColor Red
}

Write-Host ""
Write-Host "Session ID: $sessionId" -ForegroundColor Gray
