# Тестирование Advanced Search

## Запуск тестов

### Тесты без базы данных (unit-тесты)
Эти тесты проверяют логику алгоритмов без обращения к SQLite:

```bash
# Все unit-тесты
go test -v -run 'TestReciprocalRankFusion|TestQueryExpansion|TestDeduplication|TestContextResolution'

# Отдельные тесты
go test -v -run TestReciprocalRankFusion     # RRF алгоритм слияния
go test -v -run TestQueryExpansion           # Расширение запросов синонимами
go test -v -run TestDeduplication            # Дедупликация результатов
go test -v -run TestContextResolution        # Анафорическое разрешение
```

### Тесты с базой данных (integration-тесты)
Требуют компиляцию с CGO и наличие GCC:

```bash
# Установка CGO
$env:CGO_ENABLED='1'

# Тесты с базой данных
go test -v -run TestLexicalSearch           # FTS5 поиск с морфологией
go test -v -run TestMultiStageRanking       # Многоэтапное ранжирование
```

**Примечание:** На Windows требуется MinGW-w64 или TDM-GCC для SQLite тестов.

## Покрытие тестами

### ✅ Протестировано (без БД):
- **RRF (Reciprocal Rank Fusion)**: слияние лексических и семантических результатов
- **Расширение запросов**: синонимы (бюджет→бесплатно, очная→дневная)
- **Дедупликация**: удаление дублей по source_id и близости фрагментов
- **Контекстное разрешение**: анафоры ("это", "а сколько") → поиск в истории диалога

### ⏳ Требует БД:
- **Лексический поиск FTS5**: токенизация, стемминг, BM25
- **Многоэтапный пайплайн**: 6 стадий от retrieval до top-K
- **Метаданные**: буст по specialty_code и section_title

## Результаты

```
=== RUN   TestReciprocalRankFusion
--- PASS: TestReciprocalRankFusion (0.00s)

=== RUN   TestQueryExpansion
=== RUN   TestQueryExpansion/Synonym_expansion_-_очная_форма
=== RUN   TestQueryExpansion/Synonym_expansion_-_платное
=== RUN   TestQueryExpansion/Synonym_expansion_-_бюджет
=== RUN   TestQueryExpansion/No_expansion_needed
--- PASS: TestQueryExpansion (0.00s)
    --- PASS: TestQueryExpansion/Synonym_expansion_-_очная_форма (0.00s)
    --- PASS: TestQueryExpansion/Synonym_expansion_-_платное (0.00s)
    --- PASS: TestQueryExpansion/Synonym_expansion_-_бюджет (0.00s)
    --- PASS: TestQueryExpansion/No_expansion_needed (0.00s)

PASS
ok      nomos-erudit    1.112s
```

## Интеграционное тестирование

Для полного тестирования с реальной базой данных:

1. Убедитесь, что `erudit.db` содержит данные
2. Запустите `erudit.exe` 
3. Проверьте логи:
   - `🔍 ADVANCED SEARCH` - входной запрос
   - `📊 Stage 1-6` - прохождение пайплайна
   - `✅ Final results` - итоговая выдача

## Настройка параметров

В `advanced_search.go`:

```go
const (
    rrfK = 60                    // RRF параметр (стандарт: 60)
    minScoreThreshold = 0.2      // Минимальный релевантность для фильтрации
    evidenceKeywordThreshold = 0.4  // Порог совпадения ключевых слов
)

// Контекстные бусты
contextBoost := 0.15            // Малый контекст (+1 фраза из истории)
contextBoost := 0.3             // Большой контекст (+2-3 фразы)
specialtyBoost := 0.4           // Совпадение specialty_code
sectionTitleBoost := 0.2        // Максимум за section_title
```

## Troubleshooting

### CGO ошибка при компиляции
```
cgo: C compiler "gcc" not found
```
**Решение:** 
- Установите MinGW-w64: `winget install GnuWin32.MinGW`
- Или отключите SQLite тесты: используйте только unit-тесты без БД

### Кодировка кириллицы
```
redundant or: q == "�������������"
```
**Решение:** Файл сохранен не в UTF-8. Пересохраните с кодировкой UTF-8 BOM или UTF-8.

### Тесты падают с "stub"
```
Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo
```
**Решение:** Это нормально для тестов с БД. Запускайте только unit-тесты или включите CGO.
