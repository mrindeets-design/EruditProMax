package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestDiagnosticTeachersContent диагностический тест для проверки содержимого
func TestDiagnosticTeachersContent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping diagnostic test in short mode")
	}
	
	cfg := Config{
		NOMOSBase: "https://college-nomos.ru",
		CacheTime: 1 * time.Minute,
	}
	
	source := Source{
		Name: "Преподаватели",
		URL:  "https://college-nomos.ru/teachers/",
		Kind: "teachers",
	}
	
	ctx := context.Background()
	page, err := fetchPage(ctx, source, cfg.CacheTime)
	if err != nil {
		t.Fatalf("fetchPage() error: %v", err)
	}
	
	// Выводим первые 2000 символов текста
	preview := page.Text
	if len(preview) > 2000 {
		preview = preview[:2000]
	}
	
	fmt.Printf("=== Extracted text preview (first 2000 chars) ===\n")
	fmt.Printf("%s\n", preview)
	fmt.Printf("=== End of preview ===\n\n")
	
	// Проверяем нормализацию
	normalized := normalize(preview)
	fmt.Printf("=== Normalized preview (first 500 chars) ===\n")
	if len(normalized) > 500 {
		fmt.Printf("%s\n", normalized[:500])
	} else {
		fmt.Printf("%s\n", normalized)
	}
	fmt.Printf("=== End normalized ===\n\n")
	
	// Проверяем, есть ли "богитова" в тексте
	if strings.Contains(strings.ToLower(page.Text), "богитова") {
		fmt.Printf("✓ Found 'богитова' in text\n")
		
		// Найдем контекст вокруг
		idx := strings.Index(strings.ToLower(page.Text), "богитова")
		start := idx - 100
		if start < 0 {
			start = 0
		}
		end := idx + 200
		if end > len(page.Text) {
			end = len(page.Text)
		}
		
		fmt.Printf("\nContext around 'богитова':\n%s\n", page.Text[start:end])
	} else {
		fmt.Printf("✗ 'богитова' NOT found in text\n")
		
		// Попробуем найти другие имена
		testNames := []string{"преподаватель", "учитель", "олег", "юлия", "информатика"}
		for _, name := range testNames {
			if strings.Contains(strings.ToLower(page.Text), name) {
				fmt.Printf("  ✓ Found '%s'\n", name)
			}
		}
	}
}
