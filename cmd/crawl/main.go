package main

import (
	"database/sql"
	"flag"
	"log"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	log.Println("=== 🔄 Краулер сайта колледжа «Номос» ===")
	log.Println()

	// Флаги командной строки
	baseURL := flag.String("url", "", "URL для обхода (обязательный)")
	maxDepth := flag.Int("depth", 3, "Максимальная глубина обхода")
	maxPages := flag.Int("pages", 200, "Максимум страниц")
	workers := flag.Int("workers", 3, "Количество параллельных воркеров")
	dbPath := flag.String("db", "../../data/erudit.db", "Путь к базе данных")
	flag.Parse()

	if *baseURL == "" {
		log.Println("❌ Ошибка: не указан URL")
		log.Println("\nИспользование:")
		log.Println("  go run cmd/crawl/main.go -url https://college-nomos.ru")
		log.Println("\nОпции:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	log.Printf("📍 URL: %s", *baseURL)
	log.Printf("🔍 Параметры: глубина=%d, страниц=%d, воркеров=%d", *maxDepth, *maxPages, *workers)
	log.Println()

	// Создаём папку для БД
	if err := os.MkdirAll("../../data", 0755); err != nil {
		log.Fatalf("❌ Ошибка создания папки: %v", err)
	}

	// Открываем БД
	dsn := *dbPath + "?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatalf("❌ Ошибка открытия БД: %v", err)
	}
	defer db.Close()

	// Применяем миграции
	if err := runMigrations(db); err != nil {
		log.Fatalf("❌ Ошибка миграций: %v", err)
	}

	log.Printf("💾 База: %s", *dbPath)
	log.Println()

	// Создаём и запускаем краулер
	config := CrawlerConfig{
		MaxDepth:        *maxDepth,
		MaxPages:        *maxPages,
		MaxFileSize:     10 * 1024 * 1024,
		WorkerCount:     *workers,
		RequestDelay:    500 * time.Millisecond,
		RequestTimeout:  30 * time.Second,
		AllowedHosts:    []string{},
		SkipExtensions:  []string{".jpg", ".jpeg", ".png", ".gif", ".css", ".js", ".xml", ".zip", ".rar", ".ico"},
		SkipPaths:       []string{"/bitrix/", "/upload/iblock/", "/local/", "/ajax/"},
		FollowRedirects: true,
		MaxRedirects:    5,
	}

	crawler := NewCrawler(db, *baseURL, config)

	log.Println("🚀 Запуск обхода...")
	log.Println()

	if err := crawler.Start(); err != nil {
		log.Fatalf("❌ Ошибка обхода: %v", err)
	}

	log.Println()
	log.Println("✅ Обход завершён успешно!")
}

type CrawlerConfig struct {
	MaxDepth        int
	MaxPages        int
	MaxFileSize     int64
	WorkerCount     int
	RequestDelay    time.Duration
	RequestTimeout  time.Duration
	AllowedHosts    []string
	SkipExtensions  []string
	SkipPaths       []string
	FollowRedirects bool
	MaxRedirects    int
}

// Заглушки - реальная реализация в основном пакете
func NewCrawler(db *sql.DB, baseURL string, config CrawlerConfig) interface{} {
	log.Fatal("❌ Эта команда устарела. Используйте: go run . crawl -url " + baseURL)
	return nil
}

func runMigrations(db *sql.DB) error {
	log.Fatal("❌ Эта команда устарела. Используйте: go run . crawl -url <URL>")
	return nil
}
