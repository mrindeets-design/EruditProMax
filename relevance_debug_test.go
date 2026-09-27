package main

import (
	"fmt"
	"strings"
	"testing"
)

// TestCalculateRelevanceDebug детальная отладка calculateRelevance
func TestCalculateRelevanceDebug(t *testing.T) {
	query := SearchQuery{
		Text:       "Какие предметы ведёт Богитова Юлия Олеговна?",
		Topic:      "staff",
		Entities:   map[string]string{},
		Conditions: []string{},
	}
	
	fragment := `
Богитова Юлия Олеговна
Богитова Юлия Олеговна
Администрация
Преподаватели
Преподаваемые учебные предметы, курсы, дисциплины (модули):
История, История родного края, Основы этики, История России
`
	
	fmt.Printf("=== Query ===\n")
	fmt.Printf("Text: %s\n", query.Text)
	fmt.Printf("Topic: %s\n", query.Topic)
	fmt.Printf("Entities: %v\n", query.Entities)
	fmt.Printf("Conditions: %v\n\n", query.Conditions)
	
	// Извлекаем ключевые слова
	keywords := extractKeywords(query.Text)
	fmt.Printf("=== Keywords ===\n")
	fmt.Printf("%v\n\n", keywords)
	
	// Нормализуем фрагмент
	fragNorm := normalize(fragment)
	fmt.Printf("=== Normalized Fragment ===\n")
	fmt.Printf("%s\n\n", fragNorm)
	
	// Проверяем каждое ключевое слово
	fmt.Printf("=== Keyword Matching ===\n")
	kwScore := 0.0
	for _, kw := range keywords {
		found := strings.Contains(fragNorm, kw)
		if found {
			kwScore += 0.2
		}
		fmt.Printf("  '%s' -> %v (score: +%.1f)\n", kw, found, 0.2)
	}
	fmt.Printf("Total keyword score: %.2f\n\n", kwScore)
	
	// Проверяем топик
	topicKeywords := getTopicKeywords(query.Topic)
	fmt.Printf("=== Topic Keywords (topic=%s) ===\n", query.Topic)
	fmt.Printf("%v\n", topicKeywords)
	
	topicMatches := 0
	for _, kw := range topicKeywords {
		if strings.Contains(fragNorm, kw) {
			topicMatches++
			fmt.Printf("  '%s' -> MATCH\n", kw)
		}
	}
	topicScore := 0.0
	if len(topicKeywords) > 0 {
		topicScore = 0.3 * float64(topicMatches) / float64(len(topicKeywords))
	}
	fmt.Printf("Topic score: %.2f (%d/%d matches)\n\n", topicScore, topicMatches, len(topicKeywords))
	
	// Вызываем реальную функцию
	relevance := calculateRelevance(query, fragment)
	fmt.Printf("=== Final Relevance ===\n")
	fmt.Printf("%.3f\n", relevance)
	
	if relevance == 0.0 {
		t.Error("Relevance is 0.0 for a clearly relevant fragment!")
	}
}
