# Тестирование API чат-бота
$headers = @{
    "Content-Type" = "application/json; charset=utf-8"
}

# Тест 1: Список специальностей
Write-Host "=== Тест 1: Список специальностей ===" -ForegroundColor Cyan
$body1 = @{
    question = "Какие специальности есть?"
} | ConvertTo-Json -Compress

$response1 = Invoke-RestMethod -Uri "http://localhost:3000/api/chat" -Method Post -Headers $headers -Body $body1 -ContentType "application/json; charset=utf-8"
Write-Host "Ответ: $($response1.reply)" -ForegroundColor Green
Write-Host ""

# Тест 2: Стоимость обучения
Write-Host "=== Тест 2: Стоимость обучения ===" -ForegroundColor Cyan
$body2 = @{
    question = "Сколько стоит дизайн?"
} | ConvertTo-Json -Compress

$response2 = Invoke-RestMethod -Uri "http://localhost:3000/api/chat" -Method Post -Headers $headers -Body $body2 -ContentType "application/json; charset=utf-8"
Write-Host "Ответ: $($response2.reply)" -ForegroundColor Green
Write-Host ""

# Тест 3: Офф-топик
Write-Host "=== Тест 3: Офф-топик вопрос ===" -ForegroundColor Cyan
$body3 = @{
    question = "Какая погода?"
} | ConvertTo-Json -Compress

$response3 = Invoke-RestMethod -Uri "http://localhost:3000/api/chat" -Method Post -Headers $headers -Body $body3 -ContentType "application/json; charset=utf-8"
Write-Host "Ответ: $($response3.reply)" -ForegroundColor Green
Write-Host ""

# Тест 4: Директор
Write-Host "=== Тест 4: Кто директор? ===" -ForegroundColor Cyan
$body4 = @{
    question = "Кто директор колледжа?"
} | ConvertTo-Json -Compress

$response4 = Invoke-RestMethod -Uri "http://localhost:3000/api/chat" -Method Post -Headers $headers -Body $body4 -ContentType "application/json; charset=utf-8"
Write-Host "Ответ: $($response4.reply)" -ForegroundColor Green
Write-Host ""

Write-Host "=== Все тесты завершены ===" -ForegroundColor Yellow
