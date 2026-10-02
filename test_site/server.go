package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	port := "8080"
	if len(os.Args) > 1 {
		port = os.Args[1]
	}

	fs := http.FileServer(http.Dir("."))
	http.Handle("/", fs)

	addr := ":" + port
	fmt.Printf("🌐 Тестовый сервер запущен: http://localhost%s\n", addr)
	fmt.Println("Нажмите Ctrl+C для остановки")
	
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
