// Command mockserver is a tiny fixed-response HTTP server used only to record
// Arbor's demos. It returns deterministic JSON so recordings are reproducible
// and need no network access.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	addr := ":8080"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/users/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":1,"name":"Ada Lovelace","email":"ada@example.com","active":true}`)
	})
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":42,"name":"Grace Hopper","created":true}`)
	})

	log.Fatal(http.ListenAndServe(addr, mux))
}
