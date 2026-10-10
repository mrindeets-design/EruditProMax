package main

import (
	"log"
	"regexp"
	"strings"
)

// VerificationResult содержит результат проверки ответа
type VerificationResult struct {
	Passed           bool
	UnsupportedClaims []UnsupportedClaim
	Warnings         []string
}

// UnsupportedClaim описывает утверждение без подтверждения в evidence
type UnsupportedClaim struct {
	Type     string // "price", "name", "phone", "date", "address"
	Value    string
	Context  string // Контекст из ответа
}

// Claim представляет извлечённое утверждение из ответа
type Claim struct {
	Type    string
	Value   string
	Context string
}

// extractClaims извлекает фактические утверждения из ответа
func extractClaims(answer string) []Claim {
	claims := []Claim{}
	
	// 1. Извлечение цен (с ₽ или руб)
	pricePattern := regexp.MustCompile(`(\d[\d\s]*\d|\d+)\s*(₽|руб|рубл)`)
	priceMatches := pricePattern.FindAllStringSubmatch(answer, -1)
	for _, match := range priceMatches {
		price := strings.ReplaceAll(match[1], " ", "")
		claims = append(claims, Claim{
			Type:    "price",
			Value:   price,
			Context: match[0],
		})
	}
	
	// 2. Извлечение телефонов
	phonePattern := regexp.MustCompile(`\+?[78][\s-]?\(?\d{3}\)?[\s-]?\d{3}[\s-]?\d{2}[\s-]?\d{2}`)
	phoneMatches := phonePattern.FindAllString(answer, -1)
	for _, phone := range phoneMatches {
		normalized := normalizePhone(phone)
		claims = append(claims, Claim{
			Type:    "phone",
			Value:   normalized,
			Context: phone,
		})
	}
	
	// 3. Извлечение email
	emailPattern := regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	emailMatches := emailPattern.FindAllString(answer, -1)
	for _, email := range emailMatches {
		claims = append(claims, Claim{
			Type:    "email",
			Value:   strings.ToLower(email),
			Context: email,
		})
	}
	
	// 4. Извлечение специфических числовых значений в контексте
	// Например: "3 года", "60 000 часов"
	numericPattern := regexp.MustCompile(`(\d+(?:\s+\d+)*)\s+(год|лет|месяц|час|человек|мест)`)
	numericMatches := numericPattern.FindAllStringSubmatch(answer, -1)
	for _, match := range numericMatches {
		claims = append(claims, Claim{
			Type:    "numeric",
			Value:   strings.ReplaceAll(match[1], " ", "") + " " + match[2],
			Context: match[0],
		})
	}
	
	// 5. Извлечение кодов специальностей (формат: XX.XX.XX)
	codePattern := regexp.MustCompile(`\d{2}\.\d{2}\.\d{2}`)
	codeMatches := codePattern.FindAllString(answer, -1)
	for _, code := range codeMatches {
		claims = append(claims, Claim{
			Type:    "specialty_code",
			Value:   code,
			Context: code,
		})
	}
	
	log.Printf("VERIFICATION: Extracted %d claims from answer", len(claims))
	return claims
}

// normalizePhone нормализует телефон для сравнения
func normalizePhone(phone string) string {
	// Убираем все кроме цифр
	digits := regexp.MustCompile(`\d+`).FindAllString(phone, -1)
	normalized := strings.Join(digits, "")
	
	// Приводим к единому формату (10 цифр без кода страны)
	if len(normalized) == 11 && (normalized[0] == '7' || normalized[0] == '8') {
		normalized = normalized[1:]
	}
	return normalized
}

// verifyAnswer проверяет ответ против evidence
func verifyAnswer(answer string, contextText string, sources []Source) VerificationResult {
	result := VerificationResult{
		Passed:            true,
		UnsupportedClaims: []UnsupportedClaim{},
		Warnings:          []string{},
	}
	
	claims := extractClaims(answer)
	if len(claims) == 0 {
		// Нет фактических утверждений - считаем безопасным
		log.Printf("VERIFICATION: No factual claims found, PASS")
		return result
	}
	
	// Нормализуем evidence для поиска
	evidenceNorm := normalize(contextText)
	
	for _, claim := range claims {
		supported := false
		
		switch claim.Type {
		case "price":
			// Проверяем наличие цены в evidence
			// Допускаем небольшие вариации форматирования
			priceVariants := []string{
				claim.Value,
				strings.ReplaceAll(claim.Value, " ", ""),
				claim.Value + " руб",
				claim.Value + "₽",
			}
			for _, variant := range priceVariants {
				if strings.Contains(evidenceNorm, normalize(variant)) {
					supported = true
					break
				}
			}
			
		case "phone":
			// Проверяем телефон
			if strings.Contains(evidenceNorm, claim.Value) {
				supported = true
			}
			
		case "email":
			// Проверяем email
			if strings.Contains(evidenceNorm, claim.Value) {
				supported = true
			}
			
		case "specialty_code":
			// Проверяем код специальности
			if strings.Contains(evidenceNorm, claim.Value) {
				supported = true
			}
			
		case "numeric":
			// Проверяем числовые значения
			if strings.Contains(evidenceNorm, normalize(claim.Value)) {
				supported = true
			}
		}
		
		if !supported {
			log.Printf("VERIFICATION: UNSUPPORTED claim - type=%s value='%s' context='%s'", 
				claim.Type, claim.Value, claim.Context)
			result.UnsupportedClaims = append(result.UnsupportedClaims, UnsupportedClaim{
				Type:    claim.Type,
				Value:   claim.Value,
				Context: claim.Context,
			})
			result.Passed = false
		} else {
			log.Printf("VERIFICATION: SUPPORTED claim - type=%s value='%s'", claim.Type, claim.Value)
		}
	}
	
	if result.Passed {
		log.Printf("VERIFICATION: PASS - all %d claims supported", len(claims))
	} else {
		log.Printf("VERIFICATION: FAIL - %d/%d claims unsupported", len(result.UnsupportedClaims), len(claims))
	}
	
	return result
}
