package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestEncodingChain проверяет всю цепочку обработки UTF-8
func TestEncodingChain(t *testing.T) {
	// 1. Проверка нормализации
	input := "Какие предметы ведёт Богитова Юлия Олеговна?"
	normalized := normalize(input)
	
	if !utf8.ValidString(normalized) {
		t.Errorf("Normalized string is not valid UTF-8")
	}
	
	expected := "какие предметы ведет богитова юлия олеговна"
	if normalized != expected {
		t.Errorf("normalize() failed:\n  got: %q\n  want: %q", normalized, expected)
	}
	
	// 2. Проверка tokenize
	tokens := tokenize(input)
	if len(tokens) == 0 {
		t.Error("tokenize() returned empty array")
	}
	
	hasKeyword := false
	for _, token := range tokens {
		if strings.Contains(token, "предмет") || strings.Contains(token, "богитова") {
			hasKeyword = true
			break
		}
	}
	
	if !hasKeyword {
		t.Errorf("tokenize() lost keywords: %v", tokens)
	}
	
	// 3. Проверка detectTopic
	topic := detectTopic(normalized, nil)
	if topic != "staff" {
		t.Errorf("detectTopic() = %q, want 'staff'", topic)
	}
	
	fmt.Printf("✅ Encoding chain test passed\n")
	fmt.Printf("  Input: %s\n", input)
	fmt.Printf("  Normalized: %s\n", normalized)
	fmt.Printf("  Tokens: %v\n", tokens)
	fmt.Printf("  Topic: %s\n", topic)
}

// TestTeachersPageEncoding проверяет загрузку страницы преподавателей
func TestTeachersPageEncoding(t *testing.T) {
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
	
	if len(page.Text) == 0 {
		t.Fatal("fetchPage() returned empty text")
	}
	
	if !utf8.ValidString(page.Text) {
		t.Fatal("fetchPage() returned invalid UTF-8")
	}
	
	// Проверяем, что текст содержит ожидаемые слова
	textLower := strings.ToLower(page.Text)
	keywords := []string{"преподаватель", "учитель", "кафедра", "дисциплина"}
	
	foundCount := 0
	for _, keyword := range keywords {
		if strings.Contains(textLower, keyword) {
			foundCount++
			fmt.Printf("  ✓ Found keyword: %s\n", keyword)
		}
	}
	
	if foundCount == 0 {
		// Показываем первые 500 символов для отладки
		preview := page.Text
		if len(preview) > 500 {
			preview = preview[:500]
		}
		t.Errorf("No keywords found in text. Preview:\n%s", preview)
	}
	
	fmt.Printf("✅ Teachers page encoding test passed\n")
	fmt.Printf("  URL: %s\n", source.URL)
	fmt.Printf("  Text length: %d chars\n", len(page.Text))
	fmt.Printf("  UTF-8 valid: true\n")
	fmt.Printf("  Keywords found: %d/%d\n", foundCount, len(keywords))
}

// TestRelevanceCalculation проверяет вычисление релевантности
func TestRelevanceCalculation(t *testing.T) {
	query := SearchQuery{
		Text:     "Какие предметы ведёт Богитова Юлия Олеговна?",
		Topic:    "staff",
		Entities: map[string]string{
			"teacher": "Богитова Юлия Олеговна",
		},
		Conditions: []string{"предметы", "ведёт"},
	}
	
	// Фрагмент с упоминанием преподавателя
	fragment := `
		Богитова Юлия Олеговна
		Преподаватель информатики
		Ведет дисциплины: Информатика, Программирование
	`
	
	relevance := calculateRelevance(query, fragment)
	
	if relevance == 0.0 {
		t.Errorf("calculateRelevance() returned 0.0 for relevant fragment")
		
		// Отладка: проверяем нормализацию
		fragNorm := normalize(fragment)
		queryNorm := normalize(query.Text)
		
		fmt.Printf("  Query normalized: %q\n", queryNorm)
		fmt.Printf("  Fragment normalized: %q\n", fragNorm)
		
		// Проверяем ключевые слова из extractKeywords
		keywords := extractKeywords(query.Text)
		for _, kw := range keywords {
			if strings.Contains(fragNorm, kw) {
				fmt.Printf("  ✓ Keyword '%s' found in fragment\n", kw)
			} else {
				fmt.Printf("  ✗ Keyword '%s' NOT found in fragment\n", kw)
			}
		}
	}
	
	if relevance < 0.1 {
		t.Errorf("calculateRelevance() = %.3f, expected >= 0.1 for relevant fragment", relevance)
	}
	
	fmt.Printf("✅ Relevance calculation test passed\n")
	fmt.Printf("  Query: %s\n", query.Text)
	fmt.Printf("  Relevance: %.3f\n", relevance)
}
