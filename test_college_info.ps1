# Тестирование исправлений: общежитие и режим работы
# Дата: 02.10.2026

Write-Host "================================" -ForegroundColor Cyan
Write-Host "Тест исправлений колледжа Номос" -ForegroundColor Cyan
Write-Host "================================" -ForegroundColor Cyan
Write-Host ""

$baseUrl = "http://localhost:3000/api/chat"

function Test-Question {
    param(
        [string]$question,
        [string]$expectedKeyword
    )
    
    Write-Host "Вопрос: " -NoNewline -ForegroundColor Yellow
    Write-Host $question
    
    $body = @{
        message = $question
    } | ConvertTo-Json
    
    try {
        $response = Invoke-RestMethod -Uri $baseUrl -Method Post -Body $body -ContentType "application/json; charset=utf-8"
        
        Write-Host "Ответ: " -NoNewline -ForegroundColor Green
        Write-Host $response.answer
        
        if ($response.answer -match $expectedKeyword) {
            Write-Host "✅ PASS: Найдено '$expectedKeyword'" -ForegroundColor Green
        } else {
            Write-Host "❌ FAIL: Не найдено '$expectedKeyword'" -ForegroundColor Red
        }
    } catch {
        Write-Host "❌ ERROR: $_" -ForegroundColor Red
    }
    
    Write-Host ""
    Start-Sleep -Milliseconds 500
}

# Проверяем, запущен ли сервер
Write-Host "Проверка сервера..." -ForegroundColor Cyan
try {
    $ping = Invoke-RestMethod -Uri "http://localhost:3000/api/health" -ErrorAction SilentlyContinue
    Write-Host "✅ Сервер доступен" -ForegroundColor Green
} catch {
    Write-Host "❌ Сервер не запущен. Запустите: .\erudit.exe" -ForegroundColor Red
    exit 1
}
Write-Host ""

# Тест 1: Общежитие
Write-Host "[ Тест 1/5 ] Вопрос об общежитии" -ForegroundColor Cyan
Test-Question "Есть ли общежитие?" "нет"

# Тест 2: Режим работы
Write-Host "[ Тест 2/5 ] Режим работы" -ForegroundColor Cyan
Test-Question "Когда работает колледж?" "14:00"

# Тест 3: Рабочие дни
Write-Host "[ Тест 3/5 ] Рабочие дни" -ForegroundColor Cyan
Test-Question "В какие дни работает колледж?" "понедельник"

# Тест 4: Контакты
Write-Host "[ Тест 4/5 ] Контактная информация" -ForegroundColor Cyan
Test-Question "Как с вами связаться?" "271-35-36"

# Тест 5: Комплексный вопрос
Write-Host "[ Тест 5/5 ] Комплексный вопрос" -ForegroundColor Cyan
Test-Question "Есть ли у вас общежитие и когда вы работаете?" "нет"

Write-Host "================================" -ForegroundColor Cyan
Write-Host "Тестирование завершено!" -ForegroundColor Cyan
Write-Host "================================" -ForegroundColor Cyan
