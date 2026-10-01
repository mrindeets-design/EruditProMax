package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	
	_ "modernc.org/sqlite"
)

func main() {
	log.Println("=== 🔄 Индексация сайта колледжа «Номос» ===\n")
	
	// Читаем .env
	loadDotEnv("../../.env")
	baseURL := getEnv("NOMOS_BASE_URL", "https://college-nomos.ru")
	log.Printf("📍 URL: %s", baseURL)
	
	// БД
	dbPath := "../../data/erudit.db"
	if err := os.MkdirAll("../../data", 0755); err != nil {
		log.Fatalf("❌ Ошибка создания папки: %v", err)
	}
	
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("❌ Ошибка БД: %v", err)
	}
	defer db.Close()
	
	log.Printf("💾 База: %s\n", dbPath)
	
	// Простая проверка - просто вызываем crawler из основного пакета
	// Для этого нужно импортировать функции из родительского пакета
	log.Println("⚠️  Используйте встроенную команду:")
	log.Println("   go run . --index")
	log.Println("\nИли откройте админ-панель: http://localhost:3000/admin/")
}

func loadDotEnv(path string) {
	// Упрощенная версия - используем основной код
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
