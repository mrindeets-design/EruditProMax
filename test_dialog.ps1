# -*- coding: utf-8 -*-
$session = "test_$(Get-Random)"
Write-Host "Session ID: $session" -ForegroundColor Cyan

Write-Host "`n=== TEST 1: Первый вопрос про стоимость ===" -ForegroundColor Yellow
$body1 = @{
    question = "Привет, подскажи стоимость обучения"
    session_id = $session
} | ConvertTo-Json -Depth 10

$response1 = Invoke-RestMethod -Uri "http://localhost:3000/api/chat" -Method POST -ContentType "application/json; charset=utf-8" -Body ([System.Text.Encoding]::UTF8.GetBytes($body1))
Write-Host "Ответ бота: $($response1.reply)" -ForegroundColor Green

Start-Sleep -Seconds 3

Write-Host "`n=== TEST 2: Уточнение 'дизайн' ===" -ForegroundColor Yellow
$body2 = @{
    question = "дизайн"
    session_id = $session
} | ConvertTo-Json -Depth 10

$response2 = Invoke-RestMethod -Uri "http://localhost:3000/api/chat" -Method POST -ContentType "application/json; charset=utf-8" -Body ([System.Text.Encoding]::UTF8.GetBytes($body2))
Write-Host "Ответ бота: $($response2.reply)" -ForegroundColor Green

Write-Host "`n=== ПРОВЕРКА ===" -ForegroundColor Cyan
if ($response2.reply -like "*139*200*" -or $response2.reply -like "*дизайн*") {
    Write-Host "✅ УСПЕХ! Бот понял уточнение из контекста" -ForegroundColor Green
} else {
    Write-Host "❌ ОШИБКА! Бот не понял уточнение" -ForegroundColor Red
}
