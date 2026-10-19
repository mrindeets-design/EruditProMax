# 🎓 Erudit — ИИ-помощник колледжа НОМОС

<div align="center">

![Go Version](https://img.shields.io/badge/Go-1.26.7-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/License-MIT-green.svg)
![Status](https://img.shields.io/badge/Status-Production-success)
![Database](https://img.shields.io/badge/Database-SQLite-003B57?style=flat&logo=sqlite)

Интеллектуальный чат-бот с RAG-архитектурой для Воронежского колледжа «НОМОС».  
Построен на локальной/внешней LLM с гибридным поиском и обогащёнными метаданными.

[Возможности](#-возможности) • [Установка](#-установка) • [API](#-api) • [Архитектура](#-архитектура) • [Документация](#-документация)

</div>

---

## 🎯 Возможности

### 🔍 RAG Pipeline
- **Hybrid Search**: FTS5 (лексический) + semantic (embeddings) с RRF
- **Intent Understanding**: определение типа вопроса, извлечение сущностей, разрешение анафор
- **Context-Aware**: учёт истории диалога и контекста специальностей

### 📊 Обогащённые метаданные
- **Иерархия заголовков**: JSON-массив breadcrumbs для каждого фрагмента
- **Коды специальностей**: автоматическое извлечение (54.02.01, 44.02.02, 40.02.04)
- **Учебные годы**: распознавание академических периодов (2026, 2027)
- **Типы документов**: классификация (учебный план, программа, положение)

### ⚡ Производительность
- **Smart Caching**: кеширование ответов с автоматической инвалидацией
- **Session Management**: изолированные контексты для параллельных пользователей
- **Streaming**: потоковая передача ответов через SSE
- **Rate Limiting**: защита от перегрузки

### 🌐 Интеграция
- **LLM Provider Abstraction**: Ollama (локальная) + Google Gemini + OpenAI-compatible APIs
- **Web Widget**: готовый виджет для встраивания на сайт
- **Fallback System**: заранее подготовленные ответы на типовые вопросы

---

## 📊 Статистика проекта

```
📄 Страниц проиндексировано: 81
🔍 Фрагментов создано: 5908
🏷️  С метаданными: 1132 (19.2%)

📚 Специальности:
   • 44.02.02 — Преподавание в начальных классах (30 фрагментов)
   • 40.02.04 — Юриспруденция (25 фрагментов)
   • 54.02.01 — Дизайн (19 фрагментов)
```



---

## 🚀 Установка

### Требования

- **Go** 1.26.7+
- **LLM провайдер**: Ollama (локально) или Google Gemini API / OpenAI-compatible API
- **RAM**: ~8 GB, **VRAM**: ~5 GB (для 8B модели Ollama)

### Быстрый старт

```bash
git clone https://github.com/mrindeets-design/EruditProMax.git
cd EruditProMax
copy .env.example .env  # Отредактируйте и добавьте API ключ
go build -o erudit.exe
.\erudit.exe crawl && .\erudit.exe reindex
.\erudit.exe  # Сервер: http://localhost:3000
```

---

## 📡 API

### `POST /api/chat`

```json
{
  "question": "Какие предметы ведёт Богитова?",
  "session_id": "uuid",
  "stream": false
}
```

**Ответ:**
```json
{
  "answer": "Богитова Юлия Олеговна ведёт: История...",
  "sources": [{"name": "Преподаватели", "url": "..."}],
  "session_id": "uuid",
  "needs_clarification": false
}
```

### `GET /api/chat/stream` — Streaming с SSE

---

## 🏗️ Архитектура

```
User Question → Intent Understanding → Hybrid Search (FTS5 + Embeddings)
              → Context Building → LLM Generation → Answer + Cache
```

**База данных:**
- `pages` — страницы с raw_html для переиндексации
- `fragments` — фрагменты с метаданными (specialty_code, parent_titles)
- `cache_entries` — кеш ответов с умной инвалидацией

---

## 🧪 Тестирование

```bash
go test -short -v          # Unit-тесты (автономные)
go test -v                 # Все тесты (требуют LLM)
go run final_stats.go      # Статистика БД
```

---

## 📚 Документация

- [**STATUS.md**](STATUS.md) — текущее состояние проекта
- [**SUMMARY.md**](SUMMARY.md) — итоговая сводка изменений  
- [**TESTING.md**](TESTING.md) — руководство по тестированию
- [**ERUDIT_ARCHITECTURE_AUDIT.md**](ERUDIT_ARCHITECTURE_AUDIT.md) — технический аудит

---

## 🛣️ Roadmap

- [ ] Verification layer для проверки фактов
- [ ] Унификация на hybrid search (удаление legacy)
- [ ] Трассировка всех этапов обработки
- [ ] Evaluation dataset (50+ тестовых вопросов)
- [ ] Векторный поиск с переранжированием
- [ ] Извлечение текста из PDF

---

## 📄 Лицензия

MIT License

---

## 📞 Контакты

**Репозиторий:** [github.com/mrindeets-design/EruditProMax](https://github.com/mrindeets-design/EruditProMax)

**Колледж НОМОС:**  
📍 г. Воронеж, ул. Пятницкого, 67  
📞 +7 (473) 271-35-36  
📧 vcn@college-nomos.ru

---

<div align="center">
  
**Сделано с ❤️ для Воронежского колледжа «НОМОС»**

</div>
