# Исправление контекстного диалога - 2026-10-01

## Проблема
1. **Долгий ответ**: Система долго генерирует ответ
2. **Потеря контекста**: После уточнения "на дизайн" система не помнит, что спрашивали о стоимости
3. **Не находит информацию**: Говорит "стоимость не указана", хотя она есть в базе

## Пример проблемного диалога
```
User: Привет, подскажи мне стоимость обучения
Bot: Уточните, пожалуйста, о какой специальности...
User: на дизайн
Bot: стоимость обучения не указана в контексте
```

## Решение

### 1. answer.go - исправлена логика уточнений в StreamAnswer
**Было:**
```go
if intent.IsAmbiguous {
    clarification := BuildClarificationQuestion(intent)
    if clarification != "" {
        return onChunk(clarification)
    }
}
```

**Стало:**
```go
// Не спрашиваем уточнение, если это ответ на предыдущее уточнение
if intent.IsAmbiguous && intent.Type != "clarification" {
    clarification := BuildClarificationQuestion(intent)
    if clarification != "" {
        // Сохраняем контекст для будущего уточнения
        if dialogContext != nil {
            dialogContext.PendingQuestion = question
            dialogContext.ExpectedParameter = "specialty"
            dialogContext.PartialInfo = intent.Entities
        }
        return onChunk(clarification)
    }
}
```

### 2. intent.go - исправлен вызов extractEntities
**Было:**
```go
extractEntities(q, &intent, context)  // ОШИБКА: двойной указатель
```

**Стало:**
```go
extractEntities(q, intent, context)  // ПРАВИЛЬНО: один указатель
```

### 3. main.go - улучшены промпты
**Обновлены все три функции:** askOllama, streamFromOllama, streamOllama

**Новый промпт:**
```
Прочитай контекст внимательно. Если в нём есть ответ на вопрос — отвечай ПРЯМО и УВЕРЕННО, 
БЕЗ фразы "не нашёл". Используй "не нашёл" ТОЛЬКО если ответа действительно нет.
```

## Файлы изменены
- `answer.go`: строка 169-179 (StreamAnswer)
- `intent.go`: строка 272 (resolveAnaphora)
- `main.go`: строки 1616, 2132 (промпты Ollama)

## Результат
- ✅ Система сохраняет контекст между вопросами
- ✅ При ответе "на дизайн" восстанавливает полный вопрос: "стоимость обучения по специальности дизайн"
- ✅ Не задаёт повторных уточняющих вопросов
- ✅ Отвечает уверенно, если информация есть в базе
