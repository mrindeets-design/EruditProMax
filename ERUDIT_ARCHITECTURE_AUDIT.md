# ERUDIT ARCHITECTURE AUDIT
## Полный технический аудит архитектуры и диагностика качества ответов

**Дата:** 2026-10-03  
**Проект:** EruditProMax — ИИ-ассистент Воронежского колледжа «Номос»  
**Цель:** Диагностировать причины неправильных ответов и создать план восстановления качества

---

## EXECUTIVE SUMMARY

### Текущее состояние
Проект содержит **множество поколений систем**, которые работают одновременно без явной координации:
- 2 системы поиска (hybrid + legacy)
- Множественные scoring mechanisms без единой логики
- Intent detection с контекстом из истории без верификации
- System prompt содержит только behavior (хорошо), но нет verification policy
- Кэш сохраняет ответы без проверки качества
- История диалога может переносить ошибки между сессиями

### Критические проблемы
1. **Нет единого источника истины** — факты в DB, но history может их переопределить
2. **Нет verification pipeline** — ответ уходит пользователю без проверки
3. **Двойная логика поиска** — hybrid search с fallback создает непредсказуемость
4. **Context pollution** — история содержит ответы, которые модель воспринимает как факты
5. **Невозможность диагностики** — нет трассировки какой этап допустил ошибку

### Что нужно
Не очередной фикс. Нужна **единая предсказуемая система** с явными этапами:
```
Question → Understanding → Retrieval → Evidence → Generation → Verification → Answer
```

---

---

## 1. ACTUAL RUNTIME ARCHITECTURE

### 1.1 HTTP API Entry Points

**`/api/chat`** (main.go:2047-2134)
```
handleChat
  ↓
SessionManager.GetOrCreate (session.go:41)
  ↓
Cache.Get (cache.go:96) [OPTIONAL]
  ↓
GenerateAnswer (answer.go:181)
  ↓
SessionManager.UpdateContext (session.go:68)
  ↓
Cache.Put (cache.go:160) [OPTIONAL]
```

**`/api/chat/stream`** (main.go:2136-2260)
```
handleChatStream
  ↓
SessionManager.GetOrCreate
  ↓
Cache.Get [OPTIONAL]
  ↓
StreamAnswer (answer.go:265)
  ↓
SessionManager.UpdateContext
  ↓
Cache.Put [OPTIONAL]
```

### 1.2 Answer Generation Pipeline

**`GenerateAnswer`** (answer.go:181-225)
```go
GenerateAnswer(ctx, cfg, question, dialogContext)
  ↓
  prepareAnswerContext() // answer.go:27
    ↓
    UnderstandIntent(question, dialogContext) // intent.go:83
      • resolveContextualIntent() // intent.go:244
      • resolveAnaphora() // intent.go:350
    ↓
    BuildClarificationQuestion() // intent.go:418 [IF AMBIGUOUS]
    ↓
    PrepareSearchQuery(intent) // search.go:35
    ↓
    SearchMaterialsWithHybridEngine() // hybrid_search.go:648
      ↓
      NewSearchEngine() // hybrid_search.go:34
      ↓
      FOR EACH query:
        ├─ lexicalSearch() // FTS5, hybrid_search.go:173
        └─ semanticSearch() // Embeddings, hybrid_search.go:313
      ↓
      combineResults() // hybrid_search.go:413
      ↓
      Convert to SearchResult[]
    ↓
    [FALLBACK] RetrySearchWithContext() // search.go:823
    ↓
    buildContextFromResults() // answer.go:228
  ↓
  askOllama(ctx, cfg, question, contextText, dialogContext) // main.go:1578
    ↓
    pickOllamaModel() // main.go:1361
    ↓
    buildAnswerInstructions(question) // main.go:1398
    ↓
    HTTP POST to Ollama /api/generate
  ↓
  finalizeDialogTurn() // answer.go:135
```

### 1.3 Session & Context Management

**SessionManager** (session.go:10-37)
- Хранит: `map[string]*UserSession`
- Timeout: 30 минут
- Cleanup loop: автоматический

**DialogContext** (intent.go:26-36)
```go
type DialogContext struct {
    History           []DialogTurn           // Last 5 turns
    CurrentTopic      string
    LastEntities      map[string]string      // ⚠️ Persisted across turns
    LastSources       []Source
    PendingQuestion   string
    ExpectedParameter string
    PartialInfo       map[string]string

---

## 2. SEARCH IMPLEMENTATIONS

### 2.1 Active Search Systems

#### **Hybrid Search** (hybrid_search.go)
```
SearchMaterialsWithHybridEngine
  ↓
  NewSearchEngine(db, cfg)
    • loadConfig() from search_config table
    • checkFTS5Support()
  ↓
  FOR EACH query:
    Parallel:
      • lexicalSearch (FTS5) → HybridSearchResult[]
      • semanticSearch (Embeddings) → HybridSearchResult[]
    ↓
    combineResults(lexical, semantic)
      • Weighted scoring: lexicalWeight × lexicalScore + semanticWeight × semanticScore
      • Default: 0.4 × lexical + 0.6 × semantic
    ↓
    Filter by minScoreThreshold (default: 0.3)
  ↓
  Convert HybridSearchResult → SearchResult
```

**Используется:** Если FTS5 доступен  
**Fallback:** `SearchMaterialsWithContext` (legacy)

#### **Legacy Search** (search.go:148-176)
```
SearchMaterialsWithContext
  ↓
  selectRelevantSources(cfg, query) // Source scoring
  ↓
  FOR EACH source:
    fetchPage(ctx, source, cacheTime)
  ↓
  extractRelevantFragmentsWithContext(query, pages, dialogContext)
    • Chunk text (1500 chars)
    • calculateRelevanceWithContext(query, fragment, dialogContext)
      ⚠️ Множество bonus mechanisms:
        - Keyword matching (stem + exact)
        - Entity bonus
        - Context entity bonus
        - Topic bonus
        - Anaphora resolution bonus
  ↓
  Deduplicate & sort by relevance
```

**Используется:** Как fallback если hybrid search недоступен

### 2.2 Проблема двойной логики

**Conflict:**
- Hybrid search использует FTS5 BM25 + cosine similarity
- Legacy search использует keyword matching + manual scoring
- При fallback **scoring scale меняется** — невозможно сравнивать relevance между системами
- Нет единого understanding какая система сработала для конкретного ответа

**Consequence:**
- Один и тот же вопрос может вернуть разные результаты в зависимости от доступности FTS5
- Debug невозможен — неясно почему выбран конкретный fragment

---

## 3. RELEVANCE SCORING MECHANISMS

### 3.1 Hybrid Search Scoring

**lexicalSearch** (hybrid_search.go:173)
```sql
SELECT *, bm25(fragments_fts) as bm25_score
FROM fragments_fts
WHERE fragments_fts MATCH ?
ORDER BY bm25_score
```
- Использует SQLite FTS5 BM25
- Нормализованный score 0-1

**semanticSearch** (hybrid_search.go:313)
```go
cosineSimilarity = dotProduct(queryEmbedding, fragmentEmbedding) / 
                   (magnitude(queryEmbedding) * magnitude(fragmentEmbedding))
```
- Cosine similarity 0-1
- Требует embeddings в БД

**combineResults** (hybrid_search.go:413)
```go
combinedScore = lexicalWeight × lexicalScore + semanticWeight × semanticScore
// Default: 0.4 × lexical + 0.6 × semantic
```

### 3.2 Legacy Search Scoring

**calculateRelevanceWithContext** (search.go:579-760)

Множество bonus mechanisms:
```go
score = 0.0

// 1. Keyword matching
for keyword in keywords:
    if stem(keyword) in fragment:
        score += 0.25

// 2. All keywords match bonus
if matchedKeywords == len(keywords):
    score += 0.5

// 3. Partial match bonus
if kwRatio >= 0.5:
    score += 0.3 × kwRatio

// 4. Entity bonus (from current query)
for entity in query.Entities:
    if entity in fragment:
        score += 0.3

// 5. Context entity bonus ⚠️ DANGEROUS
if dialogContext != nil:
    for entity in dialogContext.LastEntities:
        if entity in fragment:
            score += 0.2  // specialty
            score += 0.15 // other

// 6. Topic bonus
if query.Topic matches fragment indicators:
    score += 0.2

// 7-10. Additional bonuses...
```

### 3.3 Критические проблемы scoring

**Проблема 1: Слишком много бонусов**
- 10+ различных bonus mechanisms
- Итоговый score может превысить 2.0
- Невозможно понять какой бонус сработал для конкретного fragment

**Проблема 2: Context entity bonus без верификации**
```go
// Если в предыдущем ответе была ошибка:
dialogContext.LastEntities["specialty"] = "Дизайн"

// Следующий поиск получает bonus за "Дизайн" даже если вопрос про другое
```

**Проблема 3: Несовместимые шкалы**
- Hybrid: 0-1 normalized
- Legacy: 0-2+ unbounded
- Нет способа сравнить relevance между системами


}
```

**Проблема:** `LastEntities` и `History` переносят информацию между ответами без проверки актуальности.




---

## 4. INTENT DETECTION & CONTEXT RESOLUTION

### 4.1 Intent Understanding Pipeline

**UnderstandIntent** (intent.go:83-242)
```go
UnderstandIntent(question, context)
  ↓
  1. Determine type: greeting | question | clarification | topic_change
  ↓
  2. Extract topic: admission | payment | teachers | specialties | ...
  ↓
  3. Extract entities:
     - specialty
     - teacher_name
     - group
     - year
     - date
  ↓
  4. resolveContextualIntent(intent, context)
     ⚠️ Merges entities from context.LastEntities
     ⚠️ Applies anaphora resolution
  ↓
  5. Check ambiguity:
     if (topic requires entity && entity missing):
         intent.IsAmbiguous = true
```

### 4.2 Context Resolution Problems

**resolveContextualIntent** (intent.go:244-310)
```go
// Восстанавливает сущности из контекста:
if context != nil && context.LastEntities != nil:
    for key, value in context.LastEntities:
        if key not in intent.Entities:
            intent.Entities[key] = value  // ⚠️ Слепое копирование
```

**Проблема:** Нет проверки актуальности entity для текущего вопроса.

**Пример:**
```
Q1: "Стоимость дизайна?"
   → LastEntities["specialty"] = "Дизайн"

Q2: "Кто директор?"  // Не связано со специальностью
   → intent.Entities["specialty"] = "Дизайн"  // ⚠️ Ненужная entity

Q3: "А какие документы нужны?"
   → Search получает entity "Дизайн" и ищет документы для дизайна
   → Но пользователь мог спросить про документы вообще!
```

### 4.3 Anaphora Resolution

**resolveAnaphora** (intent.go:350-416)
```go
// Обрабатывает: "а", "там", "это", "такой", "она", "они"

if question starts with "а ":
    if lastTurn.Topic != "":
        intent.Topic = lastTurn.Topic
    copy entities from lastTurn
```

**Проблема:** Substring matching без границ слов может давать ложные срабатывания.

---

## 5. SYSTEM PROMPT & LLM CONFIGURATION

### 5.1 System Prompt Structure

**buildAnswerInstructions** (main.go:1398-1558)

**Структура:**
```
1. Identity: "Ты — Эрудит, помощник Воронежского колледжа «Номос»"
2. Style guidelines:
   - Обращение на "вы"
   - Без markdown, emoji
   - Без вступлений и завершений
3. Answer policy:
   - "Отвечай ПОЛНО и ПО СУЩЕСТВУ"
   - "НЕ добавляй информацию, которую НЕ СПРАШИВАЛИ"
4. Completeness rules:
   - Контакты: ВСЕ способы связи
   - Адрес: ПОЛНЫЙ адрес
5. Off-topic handling
6. Context rules:
   - "Используй ТОЛЬКО информацию из контекста"
   - "Если несколько значений → НЕ выбирай произвольное"
7. Mode-specific rules (news, teachers, specialties)
```

### 5.2 Отсутствует Verification Policy

**Нет инструкций:**
- Как проверять факты перед ответом
- Что делать с conflicting evidence
- Как обрабатывать числа (цены, даты)
- Как отвечать если evidence недостаточен

### 5.3 Prompt Construction

**askOllama** (main.go:1578-1676)
```
=== ИСТОРИЯ ДИАЛОГА (если есть) ===
Пользователь: {turn.UserMessage}
Ты ответил: {turn.BotReply}  ⚠️ МОЖЕТ СОДЕРЖАТЬ ОШИБКИ
...

=== ТЕКУЩИЙ ВОПРОС ===
ВОПРОС ПОЛЬЗОВАТЕЛЯ:
{question}

=== КОНТЕКСТ С САЙТА КОЛЛЕДЖА ===
{contextText}

=== ТВОЯ ЗАДАЧА ===
Прочитай контекст внимательно...
```

**Проблема:** История включает предыдущие ответы модели, которые могут содержать ошибки.

### 5.4 LLM Parameters

```go
{
    "temperature": 0.2,        // Низкая для фактов
    "top_p": 0.90,
    "repeat_penalty": 1.08,
    "num_ctx": 32768,         // Большой context window
    "num_predict": 350-800    // Зависит от типа вопроса
}
```

**predictionLimit** (main.go:1561-1576)
- News: 650 tokens
- Teachers: 800 tokens
- Detailed/compare: 700 tokens
- Specialties: 500 tokens
- General: 450 tokens

**Проблема:** Жесткие лимиты могут обрезать полные ответы.

---

## 6. CACHE SYSTEM

### 6.1 Cache Architecture

**CacheManager** (cache.go:44-65)
```go
type CacheKey struct {
    Question      string            // Normalized
    ContextDialog []string          // Last 3 turns
    Entities      map[string]string // LastEntities
    Model         string            // "llama3.1:8b"
    PromptVersion string            // "v3"
    IndexVersion  int64             // MAX(version_number) from fragments
}
```

**Storage:**
```sql
cache_keys (id, key_hash, created_at)
cache_entries (id, key_id, answer, sources_json, fragment_ids_json, created_at, hit_count)
```

### 6.2 Critical Cache Problems

**Проблема 1: Кэширует ВСЕ ответы без проверки качества**
```go
// В handleChat (main.go:2123):
if err == nil && len(fragmentIDs) > 0:
    cache.Put(key, answer, sources, fragmentIDs)

// Нет проверки:
// - Качества ответа
// - Наличия hallucinations
// - Корректности фактов
```

**Проблема 2: Context в cache key**
```go
CacheKey {
    Question: "стоимость",
    ContextDialog: ["Q: Расскажи про дизайн", "A: ..."],
    Entities: {"specialty": "Дизайн"}
}

// Другая сессия с тем же контекстом → cache hit
// Но контекст мог содержать ОШИБКУ из первой сессии
```

**Проблема 3: Распространение ошибок между пользователями**
```
User A:
  Q: "Стоимость дизайна?"
  A: "139 200 ₽"  ← WRONG
  → Cached

User B (другая сессия):
  Q: "Стоимость дизайна?"

---

## 7. DIALOG HISTORY MANAGEMENT

### 7.1 History Storage

**DialogTurn** (intent.go:72-79)
```go
type DialogTurn struct {
    UserMessage string
    BotReply    string   // ⚠️ Хранится полный ответ
    Intent      Intent
    Sources     []Source
    Timestamp   string
}
```

**Limits:**
```go
// finalizeDialogTurn (answer.go:172-175)
if len(dialogContext.History) > 5:
    dialogContext.History = dialogContext.History[len()-5:]
```

### 7.2 Critical Problem: History ≠ Knowledge

**Сценарий:**
```
Turn 1:
  Q: "Стоимость дизайна?"
  A: "139 200 ₽"  ← ОШИБКА (правильно: 189 200 ₽)
  → History stores wrong answer

Turn 2:
  Q: "А какие документы нужны?"
  Prompt includes:
    "Ты ответил: Стоимость дизайна 139 200 ₽"
  → Model sees wrong fact in context
  → May repeat error in new answer

Turn 3:
  Q: "Напомни стоимость"
  Model может ответить из history вместо context
```

**Проблема:** История воспринимается моделью как **достоверная информация**, но она может содержать ошибки.

---

## 8. CRITICAL BUGS & ROOT CAUSES

### 8.1 Неправильные факты в ответах

**Root Cause Chain:**
```
1. SearchMaterialsWithHybridEngine возвращает фрагменты
   ↓
2. Relevance scoring может поднять НЕПРАВИЛЬНЫЙ fragment
   → Entity bonus из dialogContext
   → Partial keyword match
   ↓
3. buildContextFromResults берет топ-8 без проверки
   ↓
4. askOllama получает mixed evidence (правильные + неправильные)
   ↓
5. LLM выбирает факт БЕЗ ВЕРИФИКАЦИИ
   ↓
6. Ответ уходит пользователю
   ↓
7. finalizeDialogTurn сохраняет НЕПРАВИЛЬНЫЙ ответ в history
   ↓
8. Cache.Put сохраняет НЕПРАВИЛЬНЫЙ ответ
   ↓
9. Следующий пользователь получает тот же неправильный ответ из кэша
```

**Нет ни одной проверки на этапах 3-8.**

### 8.2 Путаница информации

**Scenario:**
```
Q: "Преподаватели колледжа"
→ Retrieval возвращает:
  [1] Богитова Юлия Олеговна (relevance: 0.9)
  [2] Фрагмент содержит "Юлия Олеговна" (другой человек, relevance: 0.7)
  [3] Список преподавателей (relevance: 0.6)
→ buildContextFromResults берет все три
→ LLM видит противоречивую информацию
→ Может смешать информацию о разных людях
```

**Root Cause:** Нет deduplication by entity.

### 8.3 Неправильный контекст из истории

**Scenario:**
```
Q1: "Расскажи про дизайн"
    → context.LastEntities["specialty"] = "Дизайн"

Q2: "Где находится колледж?"  // Не связано с дизайном
    → resolveContextualIntent копирует:
        intent.Entities["specialty"] = "Дизайн"
    → Search bonus +0.2 за фрагменты с "Дизайн"
    → Может вернуть "адрес кафедры дизайна" вместо "адрес колледжа"
```

**Root Cause:** Слепое копирование entities без проверки релевантности к новому вопросу.

### 8.4 Короткие ответы

**Scenario:**
```
Q: "Какие специальности есть?"
A: "Право и организация социального обеспечения, Юриспруденция."
   ⚠️ Пропущены: Дизайн, Преподавание в начальных классах
```

**Root Cause:**
```go
predictionLimit(question) = 450  // tokens

// LLM останавливается по лимиту
// Нет механизма для проверки полноты ответа
```

---

## 9. ARCHITECTURAL CONFLICTS

### 9.1 Multiple Sources of Truth

**Конфликт 1: Facts**
```
Database fragments        ← Should be single source
vs
System prompt             ← Should contain only BEHAVIOR
vs
Dialog history            ← Should be context, not facts
vs
Cache                     ← May contain outdated facts
```

### 9.2 Multiple Scoring Systems

**Конфликт 2: Relevance**
```
Hybrid search:
  • FTS5 BM25 (normalized 0-1)
  • Cosine similarity (0-1)
  • Combined score (0-1)

Legacy search:
  • Keyword matching
  • 10+ bonus mechanisms
  • Score unbounded (0-2+)

→ Невозможно сравнить relevance между системами
```

### 9.3 Multiple Retrieval Paths

**Конфликт 3: Search**
```
Path A: SearchMaterialsWithHybridEngine
  → NewSearchEngine → lexicalSearch + semanticSearch

Path B (fallback): SearchMaterialsWithContext
  → selectRelevantSources → extractRelevantFragments

Path C (retry): RetrySearchWithContext
  → generateAlternativeQueries → SearchMaterialsWithContext

→ 3 разных пути с разными алгоритмами
→ Непредсказуемый результат
```

---

## 10. MISSING COMPONENTS

### 10.1 No Verification Pipeline

**Отсутствует:**
```
Generated Answer
  ↓
Extract Claims
  ↓
For each claim:
  Check against evidence
  ↓
  If supported: ✓
  If unsupported: ✗
  ↓
If any claim unsupported:
  Regenerate or flag
```

### 10.2 No Reranking

**Отсутствует:**
```
Initial retrieval (top-100)
  ↓
Reranking model (cross-encoder)
  ↓
Top-K most relevant
```

### 10.3 No Conflict Resolution

**Отсутствует:**
```
Evidence 1: "Стоимость: 139 200 ₽" (date: 2023-09-01)
Evidence 2: "Стоимость: 189 200 ₽" (date: 2024-09-01)
  ↓
Conflict detected
  ↓
Resolution strategy:
  • Prefer newer date
  • Or return both with explanation
```

### 10.4 No Debug/Trace Mode

**Отсутствует:**
```
For question:
  Show:
    • Intent detection result
    • Entities extracted
    • Search queries generated
    • Retrieved fragments (all)
    • Relevance scores
    • Selected evidence
    • Final prompt
    • Model response
    • Verification result
```

### 10.5 No Evaluation Dataset

**Отсутствует:**
```
test_cases.json:
[
  {

---

## 11. PROPOSED TARGET ARCHITECTURE

### 11.1 Clean Pipeline

```
USER QUESTION
      ↓
┌─────────────────────┐
│ 1. UNDERSTANDING    │
│  • Intent detection │
│  • Entity extraction│
│  • Anaphora (safe)  │
└──────────┬──────────┘
           ↓
┌─────────────────────┐
│ 2. QUERY PREPARATION│
│  • Build queries    │
│  • Expand synonyms  │
└──────────┬──────────┘
           ↓
┌─────────────────────┐
│ 3. RETRIEVAL        │
│  • Hybrid search    │
│  • Deduplication    │
└──────────┬──────────┘
           ↓
┌─────────────────────┐
│ 4. EVIDENCE SELECT  │
│  • Reranking        │
│  • Conflict check   │
│  • Top-K selection  │
└──────────┬──────────┘
           ↓
┌─────────────────────┐
│ 5. CONTEXT ASSEMBLY │
│  • Structure evidence│
│  • Add metadata     │
└──────────┬──────────┘
           ↓
┌─────────────────────┐
│ 6. GENERATION       │
│  • Build prompt     │
│  • Call LLM         │
└──────────┬──────────┘
           ↓
┌─────────────────────┐
│ 7. VERIFICATION     │
│  • Extract claims   │
│  • Check evidence   │
│  • Flag unsupported │
└──────────┬──────────┘
           ↓
┌─────────────────────┐
│ 8. RESPONSE         │
│  • Update history   │
│  • Cache if verified│
└─────────────────────┘
```

### 11.2 Single Source of Truth

```
KNOWLEDGE = DATABASE ONLY

System Prompt = BEHAVIOR ONLY:
  • How to answer
  • How to cite evidence
  • What to do if uncertain
  • Style guidelines

Dialog History = CONTEXT ONLY:
  • User questions (not answers)
  • Topic flow
  • Pronouns resolution
```

### 11.3 Unified Retrieval

```python
class UnifiedSearchEngine:
    
    def search(query: str, limit: int) -> List[Evidence]:
        # Stage 1: Initial retrieval
        candidates = self._retrieve(query, limit * 10)
        
        # Stage 2: Reranking
        reranked = self._rerank(query, candidates)
        
        # Stage 3: Deduplication by entity
        deduplicated = self._deduplicate_entities(reranked)
        
        # Stage 4: Conflict detection
        grouped = self._group_by_claim(deduplicated)
        
        return deduplicated[:limit]
```



---

## 12. CRITICAL ISSUES SUMMARY

| Issue | Location | Impact | Priority |
|-------|----------|--------|----------|
| **History contains wrong answers** | answer.go:135 | Propagates errors | 🔴 CRITICAL |
| **Cache stores unverified answers** | main.go:2123 | Spreads errors to other users | 🔴 CRITICAL |
| **Context entities copied blindly** | intent.go:244 | Wrong context in search | 🔴 CRITICAL |
| **No verification pipeline** | — | Cannot detect hallucinations | 🔴 CRITICAL |
| **Dual search systems** | answer.go:79, search.go:149 | Unpredictable behavior | 🟠 HIGH |
| **10+ scoring bonuses** | search.go:579 | Opaque relevance | 🟠 HIGH |
| **No debug trace** | — | Cannot diagnose errors | 🟠 HIGH |
| **No evaluation dataset** | — | Cannot measure quality | 🟠 HIGH |
| **Anaphora substring matching** | intent.go:350 | False positives | 🟡 MEDIUM |
| **No provider abstraction** | main.go:1578 | Locked to Ollama | 🟡 MEDIUM |

---

## 13. MIGRATION PLAN

### Phase 1: Audit & Baseline (Current)
- ✅ Complete architecture audit
- [ ] Create test dataset (50 questions)
- [ ] Measure baseline accuracy
- [ ] Document all error patterns

### Phase 2: Cleanup & Unification
- [ ] Remove legacy search (keep only hybrid)
- [ ] Remove hardcoded facts from prompts (if any)
- [ ] Separate history (questions only) from facts
- [ ] Add trace logging to all stages

### Phase 3: Evidence Model
- [ ] Create structured Evidence type
- [ ] Implement claim extraction
- [ ] Add conflict detection
- [ ] Build evidence formatter

### Phase 4: Verification
- [ ] Implement claim-evidence matching
- [ ] Add verification step after generation
- [ ] Update cache to only store verified answers
- [ ] Add quality metrics

### Phase 5: Testing & Validation
- [ ] Run evaluation on test dataset
- [ ] Measure improvement over baseline
- [ ] Fix regression issues
- [ ] Document results

---

## 14. IMMEDIATE ACTION ITEMS

### Week 1: Investigation & Baseline
1. **Create test dataset**
   - 50 questions covering all topics (admission, payment, teachers, specialties, contacts)
   - Expected answers + evidence IDs
   - Measure current accuracy

2. **Add trace logging**
   ```go
   type RequestTrace struct {
       Question        string
       Intent          Intent
       Queries         []SearchQuery
       RetrievedCount  int
       TopFragments    []FragmentTrace
       SelectedEvidence []int64
       GeneratedAnswer string
   }
   ```

3. **Fix history propagation**
   - Store only questions in history for context
   - Do NOT include previous answers in prompt

### Month 1: Critical Fixes
4. **Remove legacy search**
   - Keep only hybrid search
   - Remove SearchMaterialsWithContext

5. **Implement basic verification**
   - Extract claims from answer
   - Check each claim against evidence
   - Flag unsupported claims

6. **Fix cache logic**
   - Only cache verified answers
   - Add quality score to cache entries

### Month 2-3: Enhancement
7. Implement structured Evidence model
8. Add reranking stage
9. Build conflict resolution
10. Create evaluation framework

---

## 15. CONCLUSION

Проект содержит **множество поколений систем**, работающих одновременно без координации. Главная проблема — **отсутствие верификации** на всех этапах:

- Intent resolution копирует entities без проверки
- Search использует множество бонусов без обоснования
- Generation не проверяет факты против evidence
- History сохраняет ошибки и распространяет их
- Cache сохраняет непроверенные ответы

Нужна не очередная система поверх существующих.

Нужна **единая согласованная система** с явными этапами и проверками на каждом.

**Цель:** Для каждого неправильного ответа должно быть возможно определить **какой именно этап допустил ошибку**.

---

**END OF AUDIT**

