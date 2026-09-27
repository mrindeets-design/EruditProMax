package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestBogitovaPriority проверяет, что фрагмент с Богитовой получает наивысшую релевантность
func TestBogitovaPriority(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	
	cfg := Config{
		NOMOSBase: "https://college-nomos.ru",
		CacheTime: 1 * time.Minute,
	}
	
	// Создаём запрос
	intent := Intent{
		Type:       "question",
		Topic:      "staff",
		Entities:   map[string]string{},
		Conditions: []string{},
		Confidence: 1.0,
	}
	
	query := SearchQuery{
		Text:       "какие предметы ведёт богитова юлия олеговна",
		Topic:      intent.Topic,
		Entities:   intent.Entities,
		Conditions: intent.Conditions,
	}
	
	queries := []SearchQuery{query}
	
	ctx := context.Background()
	results, err := SearchMaterials(ctx, cfg, queries)
	if err != nil {
		t.Fatalf("SearchMaterials() error: %v", err)
	}
	
	if len(results) == 0 {
		t.Fatal("No results found")
	}
	
	fmt.Printf("Total results: %d\n\n", len(results))
	
	// Проверяем топ-5 результатов
	top5 := results
	if len(top5) > 5 {
		top5 = results[:5]
	}
	
	fmt.Println("=== TOP 5 RESULTS ===")
	for i, result := range top5 {
		preview := result.Fragment
		if len(preview) > 150 {
			preview = preview[:150] + "..."
		}
		fmt.Printf("[%d] relevance=%.3f: %s\n", i+1, result.Relevance, preview)
	}
	
	// Проверяем, что первый результат содержит информацию о Богитовой
	if !strings.Contains(strings.ToLower(results[0].Fragment), "богитова") {
		t.Errorf("Top result doesn't contain Богитова. Fragment: %s", results[0].Fragment[:200])
	}
	
	// Проверяем, что первый результат содержит информацию о предметах
	if !strings.Contains(strings.ToLower(results[0].Fragment), "предмет") &&
	   !strings.Contains(strings.ToLower(results[0].Fragment), "дисциплин") &&
	   !strings.Contains(strings.ToLower(results[0].Fragment), "история") {
		t.Error("Top result doesn't contain subject information")
	}
	
	fmt.Printf("\n✓ Top result contains Богитова with relevance %.3f\n", results[0].Relevance)
}
