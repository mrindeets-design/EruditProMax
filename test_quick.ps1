# Простой тест с UTF-8
$utf8 = [System.Text.Encoding]::UTF8
$question = "Какие специальности есть?"
$body = @{question = $question} | ConvertTo-Json -Compress
$bodyBytes = $utf8.GetBytes($body)

Write-Host "Тест: $question" -ForegroundColor Cyan

try {
    $response = Invoke-RestMethod -Uri "http://localhost:3000/api/chat" `
        -Method Post `
        -Body $bodyBytes `
        -ContentType "application/json; charset=utf-8" `
        -UseBasicParsing
    
    Write-Host "`nОтвет:" -ForegroundColor Green
    Write-Host $response.reply
    Write-Host "`nИсточников: $($response.sources.Count)" -ForegroundColor Yellow
} catch {
    Write-Host "Ошибка: $_" -ForegroundColor Red
}
