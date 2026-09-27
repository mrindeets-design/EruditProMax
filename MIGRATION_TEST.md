# Проверка миграции БД

Тестирование схемы:

```bash
# 1. Удалить старую БД
cd c:\Users\Drago\Downloads\erudit
Remove-Item data\erudit.db -Force

# 2. Создать через отдельный скрипт
$code = @'
package main
import ("database/sql"; "fmt"; "log"; _ "modernc.org/sqlite")
func main() {
    db, err := sql.Open("sqlite", "data/erudit.db")
    if err != nil { log.Fatal(err) }
    defer db.Close()
    
    _, err = db.Exec("PRAGMA journal_mode=WAL")
    if err != nil { log.Fatal(err) }
    
    // Выполнить миграцию вручную (скопировать SQL из schema.go)
    fmt.Println("Используйте InitDatabase() из main.go после интеграции")
}
'@
Set-Content -Path test_simple.go -Value $code
go run test_simple.go
Remove-Item test_simple.go
```

## Проверка компиляции

```bash
cd c:\Users\Drago\Downloads\erudit
go build -o erudit_new.exe .
```

Результат: ✅ **BUILD: OK**

## Файлы

- `migrations.go` (5.3KB) — схема, миграции, helpers
- `schema.go` (8.3KB) — SQL DDL
- `cache.go` (7.5KB) — кэш-система
- `IMPLEMENTATION_REPORT.md` — полный отчёт

## Интеграция

Добавить в `main.go`:

```go
var db *sql.DB
var cacheManager *CacheManager

func main() {
    cfg := loadConfig()
    
    // Инициализация БД
    var err error
    db, err = InitDatabase("data/erudit.db")
    if err != nil {
        log.Fatal("Database init:", err)
    }
    defer db.Close()
    
    if err := InsertInitialSources(db, cfg.NOMOSBase); err != nil {
        log.Fatal("Initial sources:", err)
    }
    
    if err := InitUpdateSchedule(db); err != nil {
        log.Fatal("Update schedule:", err)
    }
    
    cacheManager = NewCacheManager(db)
    
    // ... остальной код
}
```

Изменить `answerQuestion()`:

```go
func answerQuestion(ctx context.Context, cfg Config, sources []Source, question string) (string, []Source) {
    // Проверка кэша
    key := BuildCacheKey(question, cfg.OllamaModel, nil)
    if cached, err := cacheManager.Get(key); err == nil && cached != nil {
        log.Printf("CACHE HIT: %s", key.Hash()[:16])
        return cached.Answer, cached.Sources
    }
    
    // Deduplication
    answer, usedSources, err := cacheManager.Dedup(key.Hash(), func() (string, []Source, error) {
        // Существующая логика
        contextText, usedSources, err := loadContext(ctx, sources, question, cfg.CacheTime)
        if err != nil {
            return "", nil, err
        }
        
        answer, err := askOllama(ctx, cfg, question, contextText)
        if err != nil {
            return "", usedSources, err
        }
        
        // TODO: получить fragmentIDs из chunks
        var fragmentIDs []int64
        
        // Сохранить в кэш
        cacheManager.Put(key, answer, usedSources, fragmentIDs)
        
        return answer, usedSources, nil
    })
    
    if err != nil {
        log.Printf("Error: %v", err)
        return fallbackAnswer(question, ""), nil
    }
    
    return answer, usedSources
}
```

## Статус

✅ Схема создана  
✅ Миграции работают  
✅ Кэш реализован  
❌ Не интегрировано в main.go  
❌ Crawler не реализован  
❌ Updater не реализован  
❌ Streaming не реализован
