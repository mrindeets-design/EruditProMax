package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestRealRelevanceCalculation тестирует релевантность на реальных данных
func TestRealRelevanceCalculation(t *testing.T) {
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
	
	// Создаем реальный запрос
	question := "Какие предметы ведёт Богитова Юлия Олеговна?"
	intent := UnderstandIntent(question, nil)
	queries := PrepareSearchQuery(intent)
	
	if len(queries) == 0 {
		t.Fatal("PrepareSearchQuery returned empty")
	}
	
	query := queries[0]
	fmt.Printf("=== Search Query ===\n")
	fmt.Printf("Text: %s\n", query.Text)
	fmt.Printf("Topic: %s\n", query.Topic)
	fmt.Printf("Entities: %v\n", query.Entities)
	fmt.Printf("Conditions: %v\n", query.Conditions)
	fmt.Printf("\n")
	
	// Разбиваем на чанки
	chunks := splitIntoSemanticChunks(page.Text)
	fmt.Printf("Total chunks: %d\n\n", len(chunks))
	
	// Проверяем релевантность первых 10 чанков
	foundRelevant := 0
	for i, chunk := range chunks {
		if i >= 20 {
			break
		}
		
		relevance := calculateRelevance(query, chunk)
		
		preview := chunk
		if len(preview) > 100 {
			preview = preview[:100]
		}
		
		if relevance > 0.0 {
			foundRelevant++
			fmt.Printf("[%d] relevance=%.3f: %s...\n", i, relevance, preview)
			
			if strings.Contains(strings.ToLower(chunk), "богитова") {
				fmt.Printf("    ✓ Contains 'богитова'\n")
				fmt.Printf("    Full chunk:\n%s\n\n", chunk)
			}
		}
	}
	
	fmt.Printf("\nFound %d relevant chunks out of first 20\n", foundRelevant)
	
	if foundRelevant == 0 {
		t.Error("No relevant chunks found - calculateRelevance might be broken")
		
		// Дополнительная диагностика
		fmt.Printf("\n=== Diagnostic Info ===\n")
		keywords := extractKeywords(query.Text)
		fmt.Printf("Keywords extracted: %v\n", keywords)
		
		testChunk := chunks[0]
		testNorm := normalize(testChunk)
		fmt.Printf("First chunk normalized: %s\n", testNorm[:min(len(testNorm), 200)])
		
		for _, kw := range keywords {
			if strings.Contains(testNorm, kw) {
				fmt.Printf("  ✓ Keyword '%s' found in first chunk\n", kw)
			} else {
				fmt.Printf("  ✗ Keyword '%s' NOT found in first chunk\n", kw)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
