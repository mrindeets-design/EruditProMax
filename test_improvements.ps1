# Тестирование исправлений Erudit
# Проверяем улучшения качества ответов

$baseUrl = "http://localhost:3000/api/chat"

function Test-Question {
    param(
        [string]$Question,
        [string]$Description
    )
    
    Write-Host "`n========================================" -ForegroundColor Cyan
    Write-Host "ТЕСТ: $Description" -ForegroundColor Cyan
    Write-Host "Вопрос: $Question" -ForegroundColor Yellow
    Write-Host "========================================" -ForegroundColor Cyan
    
    $body = @{
        question = $Question
    } | ConvertTo-Json -Compress
    
    try {
        $response = Invoke-RestMethod -Uri $baseUrl -Method Post -Body $body -ContentType "application/json; charset=utf-8"
        Write-Host "Ответ: " -ForegroundColor Green -NoNewline
        Write-Host $response.reply
        
        if ($response.sources -and $response.sources.Count -gt 0) {
            Write-Host "`nИсточники:" -ForegroundColor Gray
            foreach ($src in $response.sources) {
                Write-Host "  - $($src.name): $($src.url)" -ForegroundColor Gray
            }
        }
        
        return $response
    }
    catch {
        Write-Host "ОШИБКА: $_" -ForegroundColor Red
        return $null
    }
    
    Start-Sleep -Seconds 1
}

Write-Host "===========================================`n" -ForegroundColor Magenta
Write-Host "  ТЕСТИРОВАНИЕ ERUDIT - ИСПРАВЛЕНИЯ`n" -ForegroundColor Magenta
Write-Host "===========================================`n" -ForegroundColor Magenta

# Тест 1: Список преподавателей (раньше не работало из-за фильтрации источников)
Test-Question "Какие преподаватели работают в колледже?" "Список преподавателей"

# Тест 2: Вопрос о стоимости дизайна (раньше работало)
Test-Question "Сколько стоит обучение на дизайне?" "Стоимость дизайна"

# Тест 3: Вопрос о специальностях
Test-Question "Какие специальности есть в колледже?" "Список специальностей"

# Тест 4: Практика (новый источник)
Test-Question "Где проходит практика?" "Информация о практике"

# Тест 5: Вопрос с уточнением - часть 1
$response1 = Test-Question "Есть ли бюджетные места?" "Уточнение - шаг 1"

# Тест 6: Вопрос с уточнением - часть 2 (короткий ответ)
Start-Sleep -Seconds 2
Test-Question "Дизайн" "Уточнение - шаг 2 (короткий ответ)"

# Тест 7: Вопрос вне темы колледжа
Test-Question "Какая сегодня погода в Воронеже?" "Вопрос вне темы"

# Тест 8: Эмоциональный вопрос
Test-Question "Я не сдал экзамен, меня отчислят?!" "Эмоциональный вопрос"

# Тест 9: Справки (новый источник)
Test-Question "Как получить справку?" "Справки и документы"

# Тест 10: Пересдачи (новый источник)
Test-Question "Можно ли пересдать экзамен?" "Пересдачи"

Write-Host "`n===========================================`n" -ForegroundColor Magenta
Write-Host "  ТЕСТИРОВАНИЕ ЗАВЕРШЕНО`n" -ForegroundColor Magenta
Write-Host "===========================================`n" -ForegroundColor Magenta
