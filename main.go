// Fail: main.go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	// Eemaldasime provider impordi, kuna kasutame lokaalset Capabilities structi
)

var (
	zoneUsername string
	zoneApiKey   string
	domainFilter string
	listenAddr   string
	dryRun       bool
)

// Lokaalne Capabilities struktuur (jääb samaks)
type Capabilities struct {
	CanAdjustEndpoints bool `json:"canAdjustEndpoints"`
}

func init() {
	// Konfiguratsiooni lugemine (jääb samaks)
	flag.StringVar(&zoneUsername, "zone-username", os.Getenv("ZONEEE_API_USER"), "Zone.ee API Username (or ZONEEE_API_USER env var)")
	flag.StringVar(&zoneApiKey, "zone-api-key", os.Getenv("ZONEEE_API_KEY"), "Zone.ee API Key (or ZONEEE_API_KEY env var)")
	flag.StringVar(&domainFilter, "domain-filter", os.Getenv("ZONEEE_DOMAIN_FILTER"), "Comma separated list of exact zones to manage (or ZONEEE_DOMAIN_FILTER env var)")
	flag.StringVar(&listenAddr, "listen-addr", ":8888", "Address to listen on for webhook requests")
	flag.BoolVar(&dryRun, "dry-run", false, "Enable dry run mode (log changes without applying)")
	flag.Parse()

	if zoneUsername == "" || zoneApiKey == "" {
		log.Fatal("ERROR: Zone.ee username and API key must be provided via flags or environment variables (ZONEEE_API_USER, ZONEEE_API_KEY)")
	}
	if domainFilter == "" {
		log.Fatal("ERROR: Domain filter must be provided via -domain-filter flag or ZONEEE_DOMAIN_FILTER env var with specific zones")
	}
}

func main() {
	ctx := context.Background()

	// Domeenifiltri loomine ja valideerimine (jääb samaks)
	filters := strings.Split(domainFilter, ",")
	validFilters := []string{}
	for _, f := range filters {
		trimmed := strings.TrimSpace(f)
		if trimmed != "" {
			validFilters = append(validFilters, trimmed)
		}
	}
	if len(validFilters) == 0 {
		log.Fatal("ERROR: No valid zones found in domain filter after trimming")
	}
	df := endpoint.NewDomainFilter(validFilters)

	// Zone provideri loomine (jääb samaks)
	zoneProvider, err := NewZoneProvider(df, zoneUsername, zoneApiKey, dryRun)
	if err != nil {
		log.Fatalf("ERROR: Failed to create Zone provider: %v", err)
	}

	log.Printf("INFO: Starting Zone.ee ExternalDNS Webhook on %s", listenAddr)
	if dryRun {
		log.Println("INFO: Running in DRY RUN mode")
	}
	log.Printf("INFO: Managing zones: %v", df.Filters)

	// --- HTTP Handlerid ---

	// Juurhandler (GET /) - Tagastab provideri võimekused (jääb samaks)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			log.Printf("WARN: Received non-GET request on /: %s", r.Method)
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		log.Println("INFO: Received GET request on /")
		caps := Capabilities{
			CanAdjustEndpoints: true,
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(caps); err != nil {
			log.Printf("ERROR: Failed to encode capabilities response: %v", err)
		}
	})

	// MUUDETUD: /records endpoint käsitleb nüüd nii GET (lugemine) kui POST (muudatuste rakendamine)
	http.HandleFunc("/records", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// GET /records: Tagastab olemasolevad kirjed
			log.Println("INFO: Received GET request on /records")
			endpoints, err := zoneProvider.Records(ctx)
			if err != nil {
				log.Printf("ERROR: Failed to get records: %v", err)
				http.Error(w, "Failed to retrieve records: "+err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(endpoints); err != nil {
				log.Printf("ERROR: Failed to encode records response: %v", err)
			}
			log.Printf("INFO: Responded to GET /records with %d endpoints", len(endpoints))

		case http.MethodPost:
			// POST /records: Rakendab muudatused (ApplyChanges)
			log.Println("INFO: Received POST request on /records (ApplyChanges)")
			var changes plan.Changes
			if err := json.NewDecoder(r.Body).Decode(&changes); err != nil {
				log.Printf("ERROR: Failed to decode request body for POST /records: %v", err)
				http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
				return
			}

			err := zoneProvider.ApplyChanges(ctx, &changes)
			if err != nil {
				log.Printf("ERROR: Failed to apply changes via POST /records: %v", err)
				http.Error(w, "Failed to apply changes", http.StatusInternalServerError)
				return
			}
			log.Println("INFO: Changes applied successfully via POST /records (or logged in dry-run)")
			w.WriteHeader(http.StatusNoContent) // Edukas ApplyChanges tagastab 204

		default:
			// Muud meetodid pole /records endpointil lubatud
			log.Printf("WARN: Received unsupported method %s on /records", r.Method)
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	// MUUDETUD: /adjustendpoints endpoint (POST) - Kohandab endpoint'e (AdjustEndpoints)
	http.HandleFunc("/adjustendpoints", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			log.Printf("WARN: Received non-POST request on /adjustendpoints: %s", r.Method)
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		log.Println("INFO: Received POST request on /adjustendpoints")

		var requestedEndpoints []*endpoint.Endpoint
		if err := json.NewDecoder(r.Body).Decode(&requestedEndpoints); err != nil {
			log.Printf("ERROR: Failed to decode request body for /adjustendpoints: %v", err)
			http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
			return
		}

		adjustedEndpoints, err := zoneProvider.AdjustEndpoints(requestedEndpoints)
		if err != nil {
			log.Printf("ERROR: Failed to adjust endpoints via /adjustendpoints: %v", err)
			http.Error(w, "Failed to adjust endpoints: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(adjustedEndpoints); err != nil {
			log.Printf("ERROR: Failed to encode adjusted endpoints response: %v", err)
		}
		log.Printf("INFO: Responded to /adjustendpoints with %d adjusted endpoints", len(adjustedEndpoints))
	})

	// EEMALDATUD: Vana /apply handler
	// http.HandleFunc("/apply", ...)

	// EEMALDATUD: Vana /adjust handler
	// http.HandleFunc("/adjust", ...)

	// Tervisekontrolli endpoint (jääb samaks)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	// --- Serveri Käivitamine ---
	log.Printf("INFO: Starting server...")
	if err := http.ListenAndServe(listenAddr, nil); err != nil {
		log.Fatalf("ERROR: Failed to start HTTP server: %v", err)
	}
}
