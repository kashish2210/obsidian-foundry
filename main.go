// Command pocketful serves the Pocketful payments API.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"pocketful/internal/httpapi"
	"pocketful/internal/store"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           httpapi.New(store.New()),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("listening on %s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}
