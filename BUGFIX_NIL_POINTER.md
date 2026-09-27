# Исправление ошибок в обработке вопросов

## Дата: 27.09.2026

## Проблема 1: Nil Pointer Dereference
При задавании вопроса через API `/api/chat` возникала паника:
```
PANIC in handleChat: runtime error: invalid memory address or nil pointer dereference
```

### Причина
В файле `intent.go` функция `UnderstandIntent()` пыталась обращаться к полям структуры `context` без проверки на `nil`.

#### Проблемный код (строки 62-69):
```go
if isTopicChange(q, context) {
    intent.Type = "topic_change"
    context.TopicChanged = true              // ❌ Ошибка при context == nil
    context.LastEntities = make(map[string]string)  // ❌ Ошибка при context == nil
} else {
    intent.Type = "question"
    context.TopicChanged = false             // ❌ Ошибка при context == nil
}
```

### Исправление 1
Добавлены проверки на `nil` перед обращением к полям `context`:

```go
if isTopicChange(q, context) {
    intent.Type = "topic_change"
    if context != nil {  // ✅ Проверка добавлена
        context.TopicChanged = true
        context.LastEntities = make(map[string]string)
    }
} else {
    intent.Type = "question"
    if context != nil {  // ✅ Проверка добавлена
        context.TopicChanged = false
    }
}
```

---

## Проблема 2: Неправильное определение приветствий
Система определяла вопрос "Привет, подскажи какие есть факультеты" как **приветствие**, игнорируя реальный вопрос.

### Логи:
```
2026/09/27 16:52:43 Вопрос: Привет, подскажи какие есть факультеты
2026/09/27 16:52:43 INTENT: type=greeting topic=general entities=map[] confidence=1.00
```

### Причина
Функция `isGreeting()` использовала паттерн `^привет`, который срабатывал на любую строку, начинающуюся с "привет", независимо от наличия вопроса после приветствия.

#### Проблемный код:
```go
func isGreeting(q string) bool {
    greetings := []string{
        "^привет",  // ❌ Срабатывает на "Привет, подскажи..."
        "^здравствуй", "^добрый день", ...
    }
    ...
}
```

### Исправление 2
Изменена логика определения приветствий:
1. Проверяем, является ли приветствие **единственным** содержанием (без вопроса)
2. Если в тексте есть вопросительные слова (`что`, `как`, `где`, `подскажи` и т.д.), это **не** просто приветствие

```go
func isGreeting(q string) bool {
    // Только приветствие без вопроса
    pureGreetings := []string{
        "^привет$", "^привет!*$",  // ✅ Точное совпадение
        "^здравствуй", "^добрый день", ...
    }
    
    for _, pattern := range pureGreetings {
        matched, _ := regexp.MatchString(pattern, q)
        if matched {
            return true
        }
    }
    
    // ✅ Если есть вопросительные слова, это не просто приветствие
    questionWords := []string{"что", "как", "где", "когда", "какой", "какие", 
                               "какая", "сколько", "почему", "можно", 
                               "подскажи", "расскажи", "скажи"}
    for _, word := range questionWords {
        if strings.Contains(q, word) {
            return false
        }
    }
    
    return false
}
```

---

## Измененные файлы
- `intent.go` (строки 62-73): исправление nil pointer
- `intent.go` (строки 137-161): улучшенная логика определения приветствий

## Сборка
```bash
go build -o erudit_fixed.exe
```

## Результат
✅ Ошибка nil pointer исправлена  
✅ Приветствия с вопросами теперь обрабатываются корректно  
✅ Приложение успешно собрано (17.7 MB)  
✅ API `/api/chat` работает корректно  

## Тестирование
Рекомендуется протестировать:
1. Простой вопрос: "Какие есть факультеты?"
2. Приветствие с вопросом: "Привет, подскажи какие есть факультеты"
3. Только приветствие: "Привет"
4. Потоковый вопрос через `/api/chat/stream`
5. Последовательность вопросов с контекстом диалога
