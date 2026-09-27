package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

// =========================================================
// DATABASE SCHEMA & MIGRATIONS
// =========================================================

const schemaVersion = 1

// InitDatabase создаёт или мигрирует схему БД
func InitDatabase(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Настройки SQLite для многопоточности и производительности
	// Устанавливаем busy_timeout сразу после открытия
	if _, err := db.Exec("PRAGMA busy_timeout=10000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("busy_timeout: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=-64000", // 64MB
		"PRAGMA foreign_keys=ON",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma: %w", err)
		}
	}

	currentVersion, err := getSchemaVersion(db)
	if err != nil {
		db.Close()
		return nil, err
	}

	log.Printf("DB schema version: %d (target: %d)", currentVersion, schemaVersion)

	if currentVersion < schemaVersion {
		if err := runMigrations(db, currentVersion); err != nil {
			db.Close()
			return nil, err
		}
	}

	return db, nil
}

func getSchemaVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow("PRAGMA user_version").Scan(&version)
	return version, err
}

func setSchemaVersion(db *sql.DB, version int) error {
	_, err := db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version))
	return err
}

func setSchemaVersionTx(tx *sql.Tx, version int) error {
	_, err := tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", version))
	return err
}

func runMigrations(db *sql.DB, fromVersion int) error {
	log.Printf("Running migrations from version %d to %d", fromVersion, schemaVersion)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	migrations := []struct {
		version int
		name    string
		sql     string
	}{
		{1, "initial_schema", migrationV1Part1 + migrationV1Part2 + migrationV1Part3},
	}

	for _, m := range migrations {
		if m.version <= fromVersion {
			continue
		}
		log.Printf("Applying migration %d: %s", m.version, m.name)
		if _, err := tx.Exec(m.sql); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}

	if err := setSchemaVersionTx(tx, schemaVersion); err != nil {
		return err
	}

	return tx.Commit()
}

// =========================================================
// HELPER FUNCTIONS
// =========================================================

// RecordStat сохраняет метрику в БД
func RecordStat(db *sql.DB, name string, value float64) error {
	_, err := db.Exec(`INSERT INTO stats (metric_name, metric_value) VALUES (?, ?)`, name, value)
	return err
}

// InsertInitialSources заполняет таблицу sources начальными данными
func InsertInitialSources(db *sql.DB, baseURL string) error {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM sources").Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		log.Printf("Sources table already populated (%d records)", count)
		return nil
	}

	log.Println("Populating initial sources...")

	sources := []struct {
		url      string
		name     string
		kind     string
		category string
		interval int
	}{
		{baseURL + "/", "Главная", "page", "general", 86400},
		{baseURL + "/abitur/", "Поступающим", "section", "admission", 3600},
		{baseURL + "/students/", "Студентам", "section", "education", 86400},
		{baseURL + "/teachers/", "Преподаватели", "section", "teachers", 86400},
		{baseURL + "/press-center/news/", "Новости", "section", "news", 1800},
		{baseURL + "/sveden/paid_edu/", "Платное обучение", "page", "admission", 86400},
		{baseURL + "/sveden/document/", "Документы", "section", "documents", 86400},
		{baseURL + "/sveden/education/", "Образование", "section", "education", 86400},
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO sources (url, name, kind, category, check_interval_seconds)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, s := range sources {
		if _, err := stmt.Exec(s.url, s.name, s.kind, s.category, s.interval); err != nil {
			return fmt.Errorf("insert source %s: %w", s.name, err)
		}
	}

	return tx.Commit()
}

// InitUpdateSchedule инициализирует расписание обновлений
func InitUpdateSchedule(db *sql.DB) error {
	schedules := []struct {
		category string
		interval int
	}{
		{"schedule", 300},   // 5 минут
		{"news", 1800},      // 30 минут
		{"admission", 3600}, // 1 час
		{"default", 86400},  // 24 часа
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)

	for _, s := range schedules {
		_, err := tx.Exec(`
			INSERT INTO update_schedule (category, interval_seconds, next_run_at)
			VALUES (?, ?, ?)
			ON CONFLICT(category) DO NOTHING
		`, s.category, s.interval, now)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}
