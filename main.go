package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	contact := newContactServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/contact", contact.submit)
	mux.HandleFunc("/api/contact/config", contact.publicConfig)
	mux.Handle("/", http.FileServer(http.Dir("./web")))
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	log.Fatal(server.ListenAndServe())
}
