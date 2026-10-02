# Test search functionality
Write-Host "`n=== TESTING SEARCH ===`n" -ForegroundColor Cyan

$tests = @(
    "Какие специальности есть в колледже?",
    "09.02.07",
    "когда начинаются занятия",
    "стоимость обучения"
)

foreach ($query in $tests) {
    Write-Host "Test: $query" -ForegroundColor Yellow
    $output = & .\erudit.exe $query 2>&1 | Out-String
    
    if ($output -match "Hybrid search found (\d+) results") {
        Write-Host "  Found: $($matches[1]) fragments" -ForegroundColor Green
    }
    
    if ($output -match "FTS5 query: original='[^']+' -> fts='([^']+)'") {
        Write-Host "  FTS5: $($matches[1])" -ForegroundColor Gray
    }
    Write-Host ""
}

Write-Host "`n=== SEARCH STATUS ===`n" -ForegroundColor Cyan
& .\erudit.exe --search-status 2>&1