# Тест новой системы индексации с обогащёнными метаданными

## Команды для тестирования

### 1. Проверка текущей версии схемы
```bash
sqlite3 erudit.db "SELECT * FROM schema_migrations ORDER BY version DESC LIMIT 1;"
```

### 2. Проверка новых полей в fragments
```bash
sqlite3 erudit.db "PRAGMA table_info(fragments);"
```

### 3. Запуск индексации (небольшая выборка для теста)
```bash
./erudit.exe -crawl
```

### 4. Проверка обогащённых фрагментов
```sql
-- Проверить фрагменты с заполненными метаданными
SELECT 
    id,
    page_id,
    chunk_index,
    section_title,
    parent_titles,
    content_type,
    specialty_code,
    academic_year,
    substr(text, 1, 100) as preview
FROM fragments 
WHERE section_title IS NOT NULL
ORDER BY id DESC 
LIMIT 10;
```

### 5. Статистика по типам контента
```sql
SELECT 
    content_type, 
    COUNT(*) as count,
    AVG(length(text)) as avg_length
FROM fragments 
WHERE content_type IS NOT NULL
GROUP BY content_type;
```

### 6. Статистика по специальностям
```sql
SELECT 
    specialty_code,
    COUNT(*) as fragment_count,
    COUNT(DISTINCT page_id) as page_count
FROM fragments 
WHERE specialty_code IS NOT NULL
GROUP BY specialty_code
ORDER BY fragment_count DESC;
```

### 7. Проверка контекста (сравнение до/после)
```sql
-- Фрагменты БЕЗ контекста (старый метод)
SELECT COUNT(*) as old_method_count 
FROM fragments 
WHERE section_title IS NULL;

-- Фрагменты С контекстом (новый метод)
SELECT COUNT(*) as new_method_count 
FROM fragments 
WHERE section_title IS NOT NULL;
```

### 8. Пример полного контекста фрагмента
```sql
SELECT 
    f.id,
    f.text,
    f.section_title,
    f.parent_titles,
    f.content_type,
    f.specialty_code,
    f.academic_year,
    p.url,
    p.title as page_title
FROM fragments f
JOIN pages p ON f.page_id = p.id
WHERE f.section_title IS NOT NULL
LIMIT 1;
```

## Ожидаемые результаты

### После миграции:
- Версия схемы должна быть **3**
- Таблица fragments должна содержать новые поля
- При индексации должны появляться логи: "✅ Created enriched fragments with metadata"

### После индексации:
- Фрагменты с `section_title` - содержат заголовок раздела
- Фрагменты с `parent_titles` - JSON массив типа ["Поступающим", "Стоимость"]
- `content_type` может быть: text, table, list, faq
- `specialty_code` - коды типа 54.02.01, 40.02.01
- `academic_year` - годы типа 2025-2026

### Качество поиска:
- Поиск "стоимость дизайн" теперь найдёт фрагменты с полным контекстом
- Можно фильтровать по специальности: `WHERE specialty_code = '54.02.01'`
- Можно фильтровать по году: `WHERE academic_year = '2025-2026'`
- Таблицы индексируются целиком с типом `content_type = 'table'`

## Откат (если что-то пошло не так)

```bash
# Сделать бэкап перед тестированием
cp erudit.db erudit.db.backup

# Откатить изменения (если нужно)
cp erudit.db.backup erudit.db
```

## Следующие шаги после успешного теста

1. Переиндексировать всю базу знаний
2. Обновить hybrid_search.go для использования метаданных
3. Реализовать морфологический словарь для русского языка
4. Добавить фильтры по специальности/году в UI
