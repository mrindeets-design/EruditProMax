# Инструкции по установке и тестированию

## Установка Ollama и модели

1. Установить Ollama: https://ollama.ai/download
2. Установить модель: `ollama pull qwen2.5:7b-instruct`
3. Проверить: `ollama list`

## Запуск

```bash
go run main.go
```

Сервер: http://localhost:3000

## Тестирование

```bash
curl -X POST http://localhost:3000/api/chat -H "Content-Type: application/json" -d "{\"message\": \"Какие специальности есть?\"}"
```

## Сравнение моделей

1. Установите модели для сравнения
2. Измените OLLAMA_MODEL в .env
3. Перезапустите сервер
4. Протестируйте на одинаковых вопросах

