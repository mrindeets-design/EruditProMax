# CRITICAL FIXES TEST PLAN
## Тесты для проверки четырёх критических исправлений

**Дата:** 2026-10-03  
**Цель:** Проверить, что критические проблемы исправлены

---

## TEST 1: History Pollution (BotReply не в промпте)

### Цель
Проверить, что предыдущий ответ модели НЕ попадает в промпт как "факт"

### Сценарий
```
Q1: "Расскажи про специальность Дизайн"
→ Получаем ответ A1

Q2: "А сколько стоит обучение?"
→ Проверяем, что поиск идёт заново через retrieval
```

### Проверка
- ✅ В логах должно быть: "КОНТЕКСТ ДИАЛОГА" (не "ИСТОРИЯ ДИАЛОГА")
- ✅ В логах НЕ должно быть: "Ты ответил: ..."
- ✅ Должно быть: "Вопрос пользователя: Расскажи про специальность Дизайн"

### Команда
```bash
curl -X POST http://localhost:8080/api/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "Расскажи про специальность Дизайн", "session_id": "test1"}'

curl -X POST http://localhost:8080/api/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "А сколько стоит обучение?", "session_id": "test1"}'
```

---

## TEST 2: Entity Relevance Check

### Цель
Проверить, что entity из предыдущего вопроса НЕ копируется, если новый вопрос про другое

### Сценарий
```
Q1: "Расскажи про Дизайн"
→ LastEntities["specialty"] = "Дизайн"

Q2: "Где находится колледж?"
→ Entity "Дизайн" НЕ должна использоваться
```

### Проверка
- ✅ В логах должно быть: "skipped entity specialty='Дизайн' (NOT relevant to new question)"
- ✅ Ответ должен содержать адрес КОЛЛЕДЖА, а не кафедры дизайна

### Команда
```bash
curl -X POST http://localhost:8080/api/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "Расскажи про Дизайн", "session_id": "test2"}'

curl -X POST http://localhost:8080/api/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "Где находится колледж?", "session_id": "test2"}'
```

---

## TEST 3: Follow-up Still Works

### Цель
Проверить, что follow-up вопросы ПО ТЕМЕ работают корректно

### Сценарий
```
Q1: "Расскажи про специальность Дизайн"
→ LastEntities["specialty"] = "Дизайн"

Q2: "А сколько стоит обучение?"
→ Entity "Дизайн" ДОЛЖНА использоваться (релевантна)
```

### Проверка
- ✅ В логах должно быть: "copied entity specialty='Дизайн' (relevant)"
- ✅ Ответ должен содержать стоимость Дизайна

### Команда
```bash
curl -X POST http://localhost:8080/api/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "Расскажи про специальность Дизайн", "session_id": "test3"}'

curl -X POST http://localhost:8080/api/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "А сколько стоит обучение?", "session_id": "test3"}'
```

---

## TEST 4: Answer Verification - Price

### Цель
Проверить, что ответ с неправильной ценой отклоняется

### Сценарий
```
Q: "Сколько стоит обучение на Дизайне?"
→ Если LLM вернёт другую цену → должна сработать verification
```

### Проверка
- ✅ В логах должно быть: "VERIFICATION: Extracted N claims from answer"
- ✅ Если цена неправильная: "VERIFICATION FAILED: N unsupported claims"
- ✅ Если verification failed: ответ должен быть safe fallback

---

## TEST 5: Cache Not Saved for Unverified

### Цель
Проверить, что непроверенный ответ НЕ сохраняется в кэш

### Проверка
- ✅ В логах первого запроса: "CACHE: NOT saving (answer not verified or no evidence)"
- ✅ В логах второго запроса: "CACHE MISS" (не "CACHE HIT")

---

## TEST 6: Cache Saved for Verified

### Цель
Проверить, что проверенный ответ СОХРАНЯЕТСЯ в кэш

### Проверка
- ✅ В логах первого запроса: "VERIFICATION PASSED: answer is safe"
- ✅ В логах первого запроса: "CACHE: Saving verified answer"
- ✅ В логах второго запроса: "CACHE HIT"

---

## TEST 7: Phone/Email Verification

### Цель
Проверить, что телефоны и email проверяются

### Проверка
- ✅ Claim type="phone" должен быть извлечён
- ✅ Если телефон правильный: "VERIFICATION: SUPPORTED claim - type=phone"
- ✅ Если телефон неправильный: "VERIFICATION: UNSUPPORTED claim - type=phone"
