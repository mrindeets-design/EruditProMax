param(
    [string]$Question = "Подскажи стоимость обучения"
)

Write-Host "`n=== ТЕСТ API: $Question ===" -ForegroundColor Cyan

$apiURL = "http://localhost:3000/api/chat"
$body = @{ message = $Question } | ConvertTo-Json -Compress

try {
    $response = Invoke-RestMethod -Uri $apiURL -Method Post -Body $body -ContentType "application/json" -TimeoutSec 90
    
    Write-Host "`n📝 ОТВЕТ:" -ForegroundColor Yellow
    Write-Host $response.reply -ForegroundColor White
    
    if ($response.sources -and $response.sources.Count -gt 0) {
        Write-Host "`n📚 ИСТОЧНИКИ ($($response.sources.Count)):" -ForegroundColor Yellow
        for ($i = 0; $i -lt $response.sources.Count; $i++) {
            Write-Host "  [$($i+1)] $($response.sources[$i].Name)" -ForegroundColor Gray
        }
    }
    
    Write-Host "`n✅ ТЕСТ ЗАВЕРШЕН`n" -ForegroundColor Green
} catch {
    Write-Host "`n❌ ОШИБКА: $_`n" -ForegroundColor Red
    exit 1
}
