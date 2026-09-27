package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestTeacherChunking проверяет разбиение страницы преподавателей на чанки
func TestTeacherChunking(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
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
	
	chunks := splitIntoSemanticChunks(page.Text)
	
	fmt.Printf("Total chunks: %d\n\n", len(chunks))
	
	// Ищем чанк с Богитовой
	foundBogitova := false
	for i, chunk := range chunks {
		if strings.Contains(strings.ToLower(chunk), "богитова") {
			foundBogitova = true
			fmt.Printf("=== Chunk %d with Bogitova ===\n", i)
			
			// Показываем первые 500 символов
			preview := chunk
			if len(preview) > 500 {
				preview = preview[:500] + "..."
			}
			fmt.Printf("%s\n\n", preview)
			
			// Проверяем, что чанк начинается с имени Богитовой
			lines := strings.Split(chunk, "\n")
			firstLine := ""
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					firstLine = line
					break
				}
			}
			
			fmt.Printf("First line: %q\n", firstLine)
			
			if !strings.Contains(strings.ToLower(firstLine), "богитова") {
				t.Errorf("Chunk with Bogitova doesn't start with her name. First line: %q", firstLine)
			}
			
			// Проверяем, есть ли информация о предметах
			if !strings.Contains(strings.ToLower(chunk), "предмет") && 
			   !strings.Contains(strings.ToLower(chunk), "дисциплин") {
				t.Error("Chunk with Bogitova doesn't contain subject information")
			}
			
			break
		}
	}
	
	if !foundBogitova {
		t.Error("No chunk with Богитова found")
	}
}
