# Инструкции по проверке унификации

## Быстрая проверка

### 1. Компиляция
```bash
cd c:\Users\Drago\Downloads\erudit
go build -o erudit.exe
```

### 2. Модульные тесты
```bash
go test -v -run TestPrepareAnswerContext
go test -v -run TestFinalizeDialogTurn
go test -v -run TestClarificationStateConsistency
```

**Ожидаемый результат:** Все 3 теста PASS

## Интеграционное тестирование с Ollama

### Подготовка
```bash
# Запустить Ollama
ollama serve

# В другом терминале запустить сервер
.\erudit.exe
```

### Сценарий 1: Приветствие (обычный режим)
```bash
curl -X POST http://localhost:8080/api/chat `
  -H "Content-Type: application/json" `
  -d '{\"message\": \"Здравствуйте\", \"session_id\": \"test1\"}'
```

**Проверка:**
- Ответ содержит "Я Эрудит"
- В логах: `DIALOG: turn added, history size=1, topic=`

### Сценарий 2: Приветствие (streaming)
```bash
curl -N -X POST http://localhost:8080/api/chat/stream `
  -H "Content-Type: application/json" `
  -d '{\"message\": \"Здравствуйте\", \"session_id\": \"test2\"}'
```

**Проверка:**
- Поток событий: `event: session`, затем `data:` chunks, затем `data: [DONE]`
- В логах: `DIALOG: turn added, history size=1, topic=`

### Сценарий 3: Уточнение + ответ (обычный режим)
```bash
# Шаг 1: Запрос уточнения
curl -X POST http://localhost:8080/api/chat `
  -H "Content-Type: application/json" `
  -d '{\"message\": \"Какая стоимость обучения?\", \"session_id\": \"test3\"}'
```

**Проверка:**
- Ответ содержит "о какой специальности"
- В логах: `ASKING CLARIFICATION: topic='payment'`
- В логах: НЕТ строки `DIALOG: turn added` (уточнение не записывается)

```bash
# Шаг 2: Ответ на уточнение
curl -X POST http://localhost:8080/api/chat `
  -H "Content-Type: application/json" `
  -d '{\"message\": \"Дизайн\", \"session_id\": \"test3\"}'
```

**Проверка:**
- Ответ содержит информацию о стоимости дизайна
- В логах: `CLARIFICATION: restored topic='payment' from PartialInfo`
- В логах: `DIALOG: turn added, history size=1, topic=payment`

### Сценарий 4: Уточнение + ответ (streaming)
```bash
# Шаг 1: Запрос уточнения
curl -N -X POST http://localhost:8080/api/chat/stream `
  -H "Content-Type: application/json" `
  -d '{\"message\": \"Какая стоимость обучения?\", \"session_id\": \"test4\"}'
```

**Проверка:**
- Поток возвращает уточняющий вопрос
- В логах: `ASKING CLARIFICATION: topic='payment'`
- В логах: НЕТ строки `DIALOG: turn added`

```bash
# Шаг 2: Ответ на уточнение
curl -N -X POST http://localhost:8080/api/chat/stream `
  -H "Content-Type: application/json" `
  -d '{\"message\": \"Юриспруденция\", \"session_id\": \"test4\"}'
```

**Проверка:**
- Поток возвращает ответ + источники
- В логах: `CLARIFICATION: restored topic='payment' from PartialInfo`
- В логах: `DIALOG: turn added, history size=1, topic=payment`

### Сценарий 5: Обрыв соединения (streaming)
```bash
# Запустить и прервать через 1 секунду (Ctrl+C)
timeout 1 curl -N -X POST http://localhost:8080/api/chat/stream `
  -H "Content-Type: application/json" `
  -d '{\"message\": \"Расскажи подробно про все специальности\", \"session_id\": \"test5\"}'
```

**Проверка:**
- В логах: НЕТ строки `Cache save error` или `DIALOG: turn added` после обрыва

## Проверка согласованности состояния

### Сравнение History после одинакового диалога

**Обычный режим:**
```bash
curl -X POST http://localhost:8080/api/chat -d '{\"message\": \"Привет\", \"session_id\": \"cmp1\"}'
curl -X POST http://localhost:8080/api/chat -d '{\"message\": \"Какие специальности?\", \"session_id\": \"cmp1\"}'
```

**Streaming:**
```bash
curl -N -X POST http://localhost:8080/api/chat/stream -d '{\"message\": \"Привет\", \"session_id\": \"cmp2\"}'
curl -N -X POST http://localhost:8080/api/chat/stream -d '{\"message\": \"Какие специальности?\", \"session_id\": \"cmp2\"}'
```

**Проверка в логах:**
- Обе сессии: `history size=1` после первого запроса
- Обе сессии: `history size=2` после второго запроса
- Обе сессии: `topic=specialties` после второго запроса

## Ожидаемые результаты

### Логи GenerateAnswer (обычный режим)
```
GenerateAnswer: START
INTENT: type=greeting topic=general entities=map[] confidence=1.00
CONTEXT UPDATE: Before update - LastEntities=map[], intent.Entities=map[]
CONTEXT UPDATE: After update - LastEntities=map[]
DIALOG: turn added, history size=1, topic=
ANSWER READY: 45ms
```

### Логи StreamAnswer (потоковый режим)
```
StreamAnswer: START
INTENT: type=greeting topic=general entities=map[] confidence=1.00
STREAM DIRECT REPLY: 1.2ms
CONTEXT UPDATE: Before update - LastEntities=map[], intent.Entities=map[]
CONTEXT UPDATE: After update - LastEntities=map[]
DIALOG: turn added, history size=1, topic=
STREAM COMPLETE: 3ms
```

**Ключевое отличие:** Только скорость и наличие `STREAM` в именах, логика идентична.

## Критерии успеха

✅ Компиляция без ошибок  
✅ Все модульные тесты проходят  
✅ Приветствия записываются в History в обоих режимах  
✅ Уточнения НЕ записываются в History  
✅ Ответы на уточнения записываются с правильным topic  
✅ History ограничивается 5 записями  
✅ Обрыв streaming не создает запись в History  
✅ Обрыв streaming не сохраняет в кэш  
✅ Состояние сессий согласовано между режимами
