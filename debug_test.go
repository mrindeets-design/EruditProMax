// +build debug

package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

func TestTeachersQuery() {
	cfg := Config{
		NOMOSBase:   "https://college-nomos.ru",
		OllamaURL:   "http://127.0.0.1:11434",
		OllamaModel: "llama3.1:8b",
		CacheTime:   10 * time.Minute,
	}
	
	question := "Какие преподаватели работают в колледже?"
	
	// 1. Понимание интента
	intent := UnderstandIntent(question, nil)
	fmt.Printf("=== INTENT ===\n")
	fmt.Printf("Type: %s\n", intent.Type)
	fmt.Printf("Topic: %s\n", intent.Topic)
	fmt.Printf("Entities: %v\n", intent.Entities)
	fmt.Printf("Confidence: %.2f\n\n", intent.Confidence)
	
	// 2. Подготовка запроса
	queries := PrepareSearchQuery(intent)
	fmt.Printf("=== QUERIES ===\n")
	fmt.Printf("Total: %d\n", len(queries))
	for i, q := range queries {
		fmt.Printf("[%d] Topic=%s, Text=%s\n", i+1, q.Topic, q.Text)
	}
	fmt.Println()
	
	// 3. Выбор источников
	sources := selectRelevantSources(cfg, queries[0])
	fmt.Printf("=== SOURCES ===\n")
	fmt.Printf("Selected: %d sources\n", len(sources))
	for i := 0; i < min(5, len(sources)); i++ {
		src := sources[i]
		score := calculateSourceScore(src, queries[0])
		fmt.Printf("[%d] score=%d: %s (%s)\n", i+1, score, src.Name, src.Kind)
	}
	fmt.Println()
	
	// 4. Загрузка одной страницы для теста
	ctx := context.Background()
	teachersSource := sources[0]
	
	for _, src := range sources {
		if src.Kind == "teachers" {
			teachersSource = src
			break
		}
	}
	
	fmt.Printf("=== FETCHING PAGE ===\n")
	fmt.Printf("URL: %s\n", teachersSource.URL)
	
	page, err := fetchPage(ctx, teachersSource, cfg.CacheTime)
	if err != nil {
		log.Fatalf("Failed to fetch page: %v", err)
	}
	
	fmt.Printf("Text length: %d chars\n", len(page.Text))
	if len(page.Text) > 500 {
		fmt.Printf("First 500 chars:\n%s\n\n", page.Text[:500])
	} else if len(page.Text) > 0 {
		fmt.Printf("Full text:\n%s\n\n", page.Text)
	} else {
		fmt.Printf("WARNING: Text is empty!\n\n")
	}
	
	// 5. Разбивка на фрагменты
	chunks := splitIntoSemanticChunks(page.Text)
	fmt.Printf("=== CHUNKS ===\n")
	fmt.Printf("Total chunks: %d\n", len(chunks))
	if len(chunks) > 0 {
		fmt.Printf("First chunk (%d chars):\n%s\n\n", len(chunks[0]), chunks[0][:min(300, len(chunks[0]))])
	}
	
	// 6. Расчет релевантности
	fmt.Printf("=== RELEVANCE ===\n")
	for i := 0; i < min(5, len(chunks)); i++ {
		chunk := chunks[i]
		rel := calculateRelevance(queries[0], chunk)
		preview := chunk
		if len(preview) > 100 {
			preview = preview[:100]
		}
		fmt.Printf("[%d] %.3f: %s...\n", i+1, rel, preview)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}


