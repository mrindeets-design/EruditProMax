# Erudit — ИИ-помощник колледжа НОМОС

Интеллектуальный чат-бот на базе локальной LLM.

## Последние улучшения ✅
- RAG (поиск релевантных фрагментов)
- Источники в API response
- Контекст 8K → 32K токенов (+300%)
- Модель 3B → 7B (+133%)

## Быстрый старт

```bash
# 1. Установить Ollama (https://ollama.ai)
# 2. Установить модель
ollama pull qwen2.5:7b-instruct

# 3. Запустить
go run main.go

# 4. Открыть
# http://localhost:3000/widget/chat.html
```

## API

```bash
curl -X POST http://localhost:3000/api/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "Какие специальности?"}'
```

## Документация
- `SUMMARY.md` — краткая сводка
- `FINAL_REPORT.md` — полный отчет
- `SETUP.md` — установка
- `TEST_QUESTIONS.md` — тесты

## Требования
- Go 1.26.7+
- Ollama + модель 7B
- ~5 GB VRAM

