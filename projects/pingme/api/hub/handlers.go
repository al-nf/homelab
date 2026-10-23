package main

import (
	"log"
	"net/http"
)

func HandleSlack(w http.ResponseWriter, r *http.Request) {
}

func HandleDiscord(w http.ResponseWriter, r *http.Request) {
	if err := sendFrame([]byte("hello world")); err != nil {
		log.Printf("failed to send frame: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func HandleGmail(w http.ResponseWriter, r *http.Request) {
}
