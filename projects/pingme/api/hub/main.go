package main

import (
	"fmt"
	"net/http"
	"log"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprint(w, "Hello, World!")
    })

    http.HandleFunc("/discord", func(w http.ResponseWriter, r *http.Request) {
        go HandleDiscord(w, r)
    })

    http.HandleFunc("/slack", func(w http.ResponseWriter, r *http.Request) {
        go HandleSlack(w, r)
    })

    http.HandleFunc("/gmail", func(w http.ResponseWriter, r *http.Request) {
        go HandleGmail(w, r)
    })

    log.Println("API running on :6767")
    log.Fatal(http.ListenAndServe(":6767", nil))
}
