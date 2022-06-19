package main

import (
        "net/http"
)

func main() {
        http.Handle("/", http.StripPrefix("/", http.FileServer(http.Dir("./web"))))
        http.ListenAndServe(":8080", nil)
}