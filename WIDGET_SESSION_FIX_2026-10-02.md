# Widget Session Management Fix - 2026-10-02

## Проблемы, которые были исправлены

### 1. Жёстко указанный localhost
**Было:** `const BACKEND_URL = "http://localhost:3000";`
- После публикации запросы обращались к localhost посетителя
- Невозможно разместить frontend и backend на разных доменах

**Стало:** `const BACKEND_URL = window.ERUDIT_API_URL || "";`
- По умолчанию используется относительный URL (одинаковый домен)
- Можно настроить через `window.ERUDIT_API_URL` для раздельного размещения

### 2. Session ID не сохранялся
**Было:** Каждый запрос отправлял только `message`, session_id терялся
- Каждый вопрос начинал новую сессию
- Контекст диалога не работал

**Стало:** 
- Session ID из ответа сохраняется в `sessionStorage`
- Последующие запросы автоматически передают сохранённый session_id
- При получении нового session_id значение обновляется

### 3. Sources не отображались
**Было:** Sources из ответа игнорировались

**Стало:** 
- Sources отображаются как кликабельные ссылки
- Безопасное создание элементов (без innerHTML)
- Ссылки открываются в новой вкладке с `rel="noopener noreferrer"`

### 4. Нет блокировки повторной отправки
**Было:** Можно было отправить несколько запросов одновременно

**Стало:**
- Input и кнопка блокируются на время запроса
- Разблокировка при успехе и при ошибке (finally)
- Фокус возвращается на input после ответа

## Внесённые изменения в chat.js

### Настройки (строки 13-19)
```javascript
const SESSION_KEY = "erudit-session-id";

// Относительный URL для размещения на одном домене
// Можно переопределить через window.ERUDIT_API_URL для раздельного размещения
const BACKEND_URL = window.ERUDIT_API_URL || "";
```

### Функция addMessage - поддержка sources (строки 74-138)
```javascript
function addMessage(text, type, sources) {
    // ... создание message ...
    
    // Добавляем источники, если есть
    if (sources && sources.length > 0) {
        const sourcesDiv = document.createElement("div");
        sourcesDiv.className = "message-sources";
        // ... стилизация ...
        
        sources.forEach((source) => {
            const link = document.createElement("a");
            link.href = source.url || "#";
            link.textContent = source.title || source.url || "Источник";
            link.target = "_blank";
            link.rel = "noopener noreferrer";
            // ... добавление ссылки ...
        });
    }
}
```

### Функция askBackend - работа с session_id (строки 199-282)
```javascript
async function askBackend(question) {
    // Получаем сохранённый session_id
    const sessionId = sessionStorage.getItem(SESSION_KEY);
    
    const body = { message: question };
    
    // Добавляем session_id если есть
    if (sessionId) {
        body.session_id = sessionId;
        console.log("📦 Используем session_id:", sessionId);
    }
    
    const response = await fetch(BACKEND_URL + "/api/chat", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body)
    });
    
    const data = await response.json();
    
    // Сохраняем или обновляем session_id
    if (data.session_id) {
        sessionStorage.setItem(SESSION_KEY, data.session_id);
        console.log("💾 Сохранён session_id:", data.session_id);
    }
    
    return {
        reply: data.reply,
        sources: data.sources || []
    };
}
```

### Функция processQuestion - блокировка и sources (строки 289-350)
```javascript
async function processQuestion(question) {
    addMessage(question, "user");
    showTyping();
    
    // Блокируем повторную отправку
    input.disabled = true;
    form.querySelector("button").disabled = true;
    
    try {
        const response = await askBackend(question);
        hideTyping();
        
        // Передаём sources в addMessage
        addMessage(response.reply, "bot", response.sources);
        
    } catch (error) {
        console.error("❌ Ошибка backend:", error);
        hideTyping();
        addMessage("Я получил ваш вопрос, но сейчас не удалось получить ответ от сервера.", "bot");
    } finally {
        // Восстанавливаем управление
        input.disabled = false;
        form.querySelector("button").disabled = false;
        input.focus();
    }
}
```

## Проверка функциональности

### Тест 1: Session ID сохраняется
```bash
.\test_widget_session.ps1
```

**Результат:**
```
SUCCESS: Session ID preserved between requests
Session ID: 651782316cfc12d38a17735b81e97bd1 (одинаковый для обоих запросов)
```

### Тест 2: Проверка в браузере
1. Открыть http://localhost:3000/widget/chat.html
2. Открыть консоль браузера (F12)
3. Задать первый вопрос
4. В консоли должен быть лог: `💾 Сохранён session_id: ...`
5. Проверить sessionStorage: `sessionStorage.getItem('erudit-session-id')`
6. Задать второй вопрос
7. В консоли должен быть лог: `📦 Используем session_id: ...`
8. Session ID должен быть тот же самый

### Тест 3: Раздельное размещение frontend/backend
Для размещения на разных доменах добавьте перед подключением скрипта:
```html
<script>
    window.ERUDIT_API_URL = 'https://api.example.com';
</script>
<script src="/widget/chat.js"></script>
```

### Тест 4: Sources отображаются
Когда backend вернёт источники, они отобразятся как кликабельные ссылки под ответом бота.

## Совместимость

- ✅ Сохранён текущий дизайн
- ✅ Совместимость встраиваемого виджета
- ✅ Работает на одном домене (по умолчанию)
- ✅ Поддержка раздельного размещения (через window.ERUDIT_API_URL)
- ✅ Безопасное отображение контента (без innerHTML)
- ✅ Защита от повторной отправки

## Файлы изменены

1. `web/widget/chat.js` - основной файл виджета
2. `test_widget_session.ps1` - тестовый скрипт для проверки

## Дополнительная информация

- Session ID хранится в sessionStorage, не в localStorage
- Session ID очищается при закрытии вкладки/браузера
- Sources безопасно создаются через createElement (XSS protection)
- Блокировка формы предотвращает race conditions
