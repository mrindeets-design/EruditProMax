# Test API with proper encoding
$headers = @{
    "Content-Type" = "application/json; charset=utf-8"
}

Write-Host "=== Тест API ===" -ForegroundColor Cyan
Write-Host ""

# Тест 1: Простой вопрос
$body = @{
    question = "Какие специальности есть в колледже?"
} | ConvertTo-Json

Write-Host "Отправка запроса..." -ForegroundColor Yellow
try {
    $response = Invoke-WebRequest -Uri "http://localhost:3000/api/chat" `
        -Method Post `
        -Headers $headers `
        -Body ([System.Text.Encoding]::UTF8.GetBytes($body)) `
        -ContentType "application/json; charset=utf-8"
    
    $result = $response.Content | ConvertFrom-Json
    Write-Host "Ответ получен:" -ForegroundColor Green
    Write-Host $result.reply -ForegroundColor White
    Write-Host ""
    Write-Host "Источники: $($result.sources.Count)" -ForegroundColor Yellow
} catch {
    Write-Host "Ошибка: $($_.Exception.Message)" -ForegroundColor Red
    if ($_.Exception.Response) {
        $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
        $reader.BaseStream.Position = 0
        $responseBody = $reader.ReadToEnd()
        Write-Host "Response: $responseBody" -ForegroundColor Red
    }
}
