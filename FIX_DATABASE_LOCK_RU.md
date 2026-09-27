# Исправление ошибки "database is locked" в Erudit

## Дата: 27.09.2026

## Проблема
При запуске Erudit возникала ошибка:
```
Applying migration 1: initial_schema
Failed to init database: database is locked (5) (SQLITE_BUSY)
```

## Причина

После анализа кода были выявлены следующие проблемы:

### 1. **Основная причина**: Вызов `setSchemaVersion(db, ...)` внутри транзакции (migrations.go:97)
```go
func runMigrations(db *sql.DB, fromVersion int) error {
    tx, err := db.Begin()  // Начинаем транзакцию
    // ... выполняем миграции через tx.Exec() ...
    
    if err := setSchemaVersion(db, schemaVersion); err != nil {  // ❌ Используем db вместо tx!
        return err
    }
    return tx.Commit()
}
```

**Проблема**: `setSchemaVersion` использует `db.Exec()` для установки `PRAGMA user_version`, в то время как активная транзакция `tx` ещё не закоммичена. Это создаёт конфликт блокировок:
- Транзакция `tx` держит write lock
- `db.Exec()` пытается получить свою блокировку
- Результат: SQLITE_BUSY

### 2. Вызов `RecordStat(cm.db, ...)` внутри транзакции (cache.go:249)
```go
func (cm *CacheManager) Put(...) error {
    tx, err := cm.db.Begin()
    // ... работа с tx ...
    RecordStat(cm.db, "cache_miss", 1)  // ❌ Используем db внутри транзакции tx
    return tx.Commit()
}
```

### 3. Синхронные вызовы `db.Exec()` в горячих путях (cache.go:132, 140)
```go
cm.db.Exec(`UPDATE cache_entries SET invalidated_at = datetime('now') WHERE id = ?`, entry.ID)
cm.db.Exec(`UPDATE cache_entries SET hit_count = hit_count + 1 ...`)
RecordStat(cm.db, "cache_hit", 1)
```

Эти операции блокировали основной поток запросов.

### 4. Порядок установки PRAGMA busy_timeout
`busy_timeout` устанавливался вместе с другими PRAGMA, а должен быть первым после открытия соединения.

## Решение

### 1. Создание `setSchemaVersionTx()` для работы внутри транзакции
```go
func setSchemaVersionTx(tx *sql.Tx, version int) error {
    _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", version))
    return err
}

func runMigrations(db *sql.DB, fromVersion int) error {
    tx, err := db.Begin()
    // ... миграции ...
    
    if err := setSchemaVersionTx(tx, schemaVersion); err != nil {  // ✅ Используем tx
        return err
    }
    return tx.Commit()
}
```

### 2. Запись статистики внутри транзакции
```go
func (cm *CacheManager) Put(...) error {
    tx, err := cm.db.Begin()
    // ... работа ...
    
    // ✅ Записываем статистику внутри транзакции
    if _, err := tx.Exec(`INSERT INTO stats (metric_name, metric_value) VALUES (?, ?)`, 
        "cache_miss", 1.0); err != nil {
        return err
    }
    
    return tx.Commit()
}
```

### 3. Асинхронное обновление метрик кэша
```go
// ✅ Асинхронно обновляем метрики, чтобы не блокировать основной запрос
go func() {
    cm.db.Exec(`UPDATE cache_entries SET hit_count = hit_count + 1, last_hit_at = datetime('now') WHERE id = ?`, entry.ID)
    RecordStat(cm.db, "cache_hit", 1)
}()
```

### 4. Приоритетная установка busy_timeout
```go
func InitDatabase(dbPath string) (*sql.DB, error) {
    db, err := sql.Open("sqlite", dbPath)
    // ...
    
    // ✅ Устанавливаем busy_timeout сразу после открытия (увеличен до 10 секунд)
    if _, err := db.Exec("PRAGMA busy_timeout=10000"); err != nil {
        db.Close()
        return nil, fmt.Errorf("busy_timeout: %w", err)
    }
    
    // Остальные PRAGMA
    pragmas := []string{
        "PRAGMA journal_mode=WAL",
        "PRAGMA synchronous=NORMAL",
        // ...
    }
}
```

## Изменённые файлы

1. **migrations.go**
   - Добавлена функция `setSchemaVersionTx(tx *sql.Tx, version int)`
   - `runMigrations()`: изменён вызов с `setSchemaVersion(db, ...)` на `setSchemaVersionTx(tx, ...)`
   - `InitDatabase()`: `busy_timeout` установлен первым и увеличен до 10000 мс

2. **cache.go**
   - `Get()`: асинхронное обновление метрик через `go func()`
   - `Put()`: запись статистики через `tx.Exec()` вместо `RecordStat(cm.db, ...)`

## Проверка

### Тест 1: Миграция на новой базе
```
✅ DB schema version: 0 (target: 1)
✅ Running migrations from version 0 to 1
✅ Applying migration 1: initial_schema
✅ Migration test SUCCESS: version=1
✅ 12 таблиц создано
```

### Тест 2: Существующая база (пропуск миграции)
```
✅ DB schema version: 1 (target: 1)
✅ Existing DB test SUCCESS: version=1
```

### Тест 3: Конкурентный доступ (3 одновременные инициализации)
```
✅ Goroutine 0: SUCCESS
✅ Goroutine 1: SUCCESS
✅ Goroutine 2: SUCCESS
✅ Version: 1
✅ Tables: 12
```

### Тест 4: Реальный запуск приложения
```
✅ DB schema version: 1 (target: 1)
✅ Cache manager initialized
✅ Erudit backend running on :3000
```

## Резервные копии

Созданы резервные копии:
- `data/erudit.db.backup` - копия перед исправлением
- Исходные данные сохранены

## Результат

✅ Ошибка "database is locked (5) (SQLITE_BUSY)" полностью устранена  
✅ Все операции одной транзакции выполняются через её `tx`  
✅ Корректные Commit/Rollback обеспечены  
✅ busy_timeout настроен с учётом SQLite-драйвера modernc.org/sqlite  
✅ WAL mode работает корректно  
✅ Существующие данные сохранены  
✅ Проверка на новой базе и копии существующей пройдена  
✅ Повторный запуск работает без ошибок  
✅ Конкурентный доступ обрабатывается корректно
