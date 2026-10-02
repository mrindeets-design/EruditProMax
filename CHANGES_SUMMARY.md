# Унификация GenerateAnswer и StreamAnswer — Сводка изменений

## Статистика

```
answer.go       | +154 -97  (рефакторинг, выделение общих функций)
main.go         | +27 -18   (улучшена обработка ошибок в кэше)
answer_test.go  | +247      (новые тесты)
```

**Итого:** +428 строк, -115 строк, устранено ~120 строк дублирования

## Ключевые изменения

### answer.go

1. **Новая структура answerContext** (строки ~50-56)
   - Инкапсулирует результат подготовки контекста
   - Флаг `ShouldGenerate` управляет ветвлением

2. **prepareAnswerContext()** (строки ~58-150)
   - Единая логика для обоих режимов
   - UnderstandIntent → проверка уточнений → поиск материалов
   - Возвращает либо DirectReply, либо контекст для генерации

3. **finalizeDialogTurn()** (строки ~152-170)
   - Единое место обновления состояния диалога
   - Обновление CurrentTopic, LastEntities, LastSources
   - Добавление DialogTurn в History
   - Ограничение History до 5 записей

4. **Рефакторинг GenerateAnswer()** (строки ~172-220)
   - Использует prepareAnswerContext()
   - Вызывает finalizeDialogTurn() ТОЛЬКО после успеха
   - Для уточнений НЕ записывает в History

5. **Рефакторинг StreamAnswer()** (строки ~222-280)
   - Использует prepareAnswerContext() (та же логика!)
   - Накапливает ответ через wrappedOnChunk
   - Вызывает finalizeDialogTurn() ТОЛЬКО после успеха
   - Для уточнений НЕ записывает в History

### main.go

6. **handleChatStream() улучшения** (строки ~2188-2230)
   - Переменная `streamError` для явной проверки
   - Сохранение в кэш ТОЛЬКО при `streamError == nil && collectedAnswer.Len() > 0`
   - Логирование ошибок кэша

### answer_test.go (новый файл)

7. **TestPrepareAnswerContext**
   - Проверка приветствий
   - Проверка уточнений
   - Проверка восстановления Intent после уточнения

8. **TestFinalizeDialogTurn**
   - Проверка добавления в History
   - Проверка ограничения до 5 записей
   - Проверка обновления CurrentTopic

9. **TestClarificationStateConsistency**
   - Сравнение поведения GenerateAnswer vs StreamAnswer
   - Проверка PendingQuestion, PartialInfo["topic"]
   - Проверка, что уточнения НЕ попадают в History

## Устраненные проблемы

### До рефакторинга

❌ StreamAnswer не сохранял DialogTurn в History  
❌ StreamAnswer не сохранял topic в PartialInfo (`PartialInfo = intent.Entities`)  
❌ ~120 строк дублированного кода подготовки контекста  
❌ handleChatStream сохранял в кэш даже при ошибке  
❌ Две разные точки обновления состояния диалога  

### После рефакторинга

✅ Оба режима используют finalizeDialogTurn()  
✅ PartialInfo["topic"] сохраняется корректно через prepareAnswerContext()  
✅ Общая функция prepareAnswerContext() — нет дублирования  
✅ Кэш сохраняется ТОЛЬКО при `streamError == nil`  
✅ Единственная точка обновления состояния — finalizeDialogTurn()  

## Гарантии согласованности

| Аспект | GenerateAnswer | StreamAnswer |
|--------|----------------|--------------|
| История приветствий | ✅ Записывается | ✅ Записывается |
| История уточнений | ✅ НЕ записывается | ✅ НЕ записывается |
| История ответов | ✅ Записывается | ✅ Записывается |
| Сохранение topic | ✅ В PartialInfo["topic"] | ✅ В PartialInfo["topic"] |
| Восстановление topic | ✅ Из PartialInfo | ✅ Из PartialInfo |
| Ограничение History | ✅ До 5 записей | ✅ До 5 записей |
| Обработка ошибок | ✅ Не сохраняет | ✅ Не сохраняет |
| Кэш при ошибке | ✅ Не сохраняет | ✅ Не сохраняет |

## Тестирование

### Модульные тесты
```bash
go test -v -run TestPrepareAnswerContext          # ✅ PASS (3 sub-tests)
go test -v -run TestFinalizeDialogTurn            # ✅ PASS (3 sub-tests)
go test -v -run TestClarificationStateConsistency # ✅ PASS
```

### Интеграционные тесты
См. `TESTING_GUIDE.md` для сценариев с Ollama

## Обратная совместимость

✅ HTTP API не изменился  
✅ SSE формат не изменился  
✅ Структура DialogContext расширена, но backward-compatible  
✅ Формат кэша не изменился  
✅ Существующие клиенты работают без изменений  

## Документация

- **UNIFICATION_REPORT.md** — техническое описание рефакторинга
- **TESTING_GUIDE.md** — инструкции по проверке
- **answer_test.go** — модульные тесты с покрытием ключевых сценариев

## Следующие шаги

1. ✅ Компиляция без ошибок
2. ✅ Модульные тесты проходят
3. ⏭️ Интеграционное тестирование с Ollama (см. TESTING_GUIDE.md)
4. ⏭️ Проверка на production workload

