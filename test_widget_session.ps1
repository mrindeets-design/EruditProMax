Write-Host ""
Write-Host "=== TEST SESSION_ID IN WIDGET ===" -ForegroundColor Cyan
Write-Host ""

$headers = @{
    "Content-Type" = "application/json; charset=utf-8"
}

# First request
Write-Host "1. First question (without session_id):" -ForegroundColor Yellow
$body1 = @{
    message = "What specialties are available?"
} | ConvertTo-Json

$response1 = Invoke-RestMethod -Uri "http://localhost:3000/api/chat" -Method Post -Headers $headers -Body ([System.Text.Encoding]::UTF8.GetBytes($body1))

Write-Host "Reply: $($response1.reply.Substring(0, [Math]::Min(100, $response1.reply.Length)))..." -ForegroundColor Green
Write-Host "Session ID: $($response1.session_id)" -ForegroundColor Cyan
Write-Host "Sources count: $($response1.sources.Count)" -ForegroundColor Cyan

if ($response1.sources -and $response1.sources.Count -gt 0) {
    Write-Host "Sources:" -ForegroundColor Magenta
    foreach ($source in $response1.sources) {
        Write-Host "  - $($source.title): $($source.url)" -ForegroundColor Gray
    }
}

Write-Host ""

# Second request with session_id
Write-Host "2. Second question (with session_id from first response):" -ForegroundColor Yellow
$sessionId = $response1.session_id

$body2 = @{
    message = "How much does education cost?"
    session_id = $sessionId
} | ConvertTo-Json

Write-Host "Sending session_id: $sessionId" -ForegroundColor Cyan

$response2 = Invoke-RestMethod -Uri "http://localhost:3000/api/chat" -Method Post -Headers $headers -Body ([System.Text.Encoding]::UTF8.GetBytes($body2))

Write-Host "Reply: $($response2.reply.Substring(0, [Math]::Min(100, $response2.reply.Length)))..." -ForegroundColor Green
Write-Host "Session ID: $($response2.session_id)" -ForegroundColor Cyan
Write-Host "Sources count: $($response2.sources.Count)" -ForegroundColor Cyan

if ($response2.sources -and $response2.sources.Count -gt 0) {
    Write-Host "Sources:" -ForegroundColor Magenta
    foreach ($source in $response2.sources) {
        Write-Host "  - $($source.title): $($source.url)" -ForegroundColor Gray
    }
}

Write-Host ""

# Verification
if ($response1.session_id -eq $response2.session_id) {
    Write-Host "SUCCESS: Session ID preserved between requests" -ForegroundColor Green
} else {
    Write-Host "ERROR: Session ID changed" -ForegroundColor Red
    Write-Host "   First: $($response1.session_id)" -ForegroundColor Red
    Write-Host "   Second: $($response2.session_id)" -ForegroundColor Red
}

Write-Host ""
Write-Host "=== Check widget in browser ===" -ForegroundColor Yellow
Write-Host "URL: http://localhost:3000/widget/chat.html" -ForegroundColor White
Write-Host ""
Write-Host "Browser test instructions:" -ForegroundColor Cyan
Write-Host "1. Open browser console (F12)" -ForegroundColor White
Write-Host "2. Ask first question" -ForegroundColor White
Write-Host "3. Check logs: should see 'Saved session_id: ...'" -ForegroundColor White
Write-Host "4. Ask second question" -ForegroundColor White
Write-Host "5. Check logs: should see 'Using session_id: ...'" -ForegroundColor White
Write-Host "6. Check sessionStorage: sessionStorage.getItem('erudit-session-id')" -ForegroundColor White
Write-Host "7. Check sources display (links)" -ForegroundColor White
Write-Host ""
