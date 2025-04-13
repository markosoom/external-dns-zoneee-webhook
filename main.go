package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"
)

// --- Konfiguratsioon (loe keskkonnamuutujatest) ---

var (
	zoneAPIURL        = "https://api.zone.eu/v2"      // Zone.eu API baas-URL
	zoneUsername      = os.Getenv("ZONE_USERNAME")    // ZoneID kasutajanimi
	zoneAPIPassword   = os.Getenv("ZONE_API_PASSWORD") // ZoneID API võti/parool
	listenPort        = getEnv("LISTEN_PORT", ":8888")  // Port, millel webhook kuulab
	domainFilter      = endpoint.NewDomainFilter(parseDomainFilter(os.Getenv("ZONE_DOMAIN_FILTER"))) // Domeenifilter external-dns jaoks
	defaultTTL        = endpoint.TTL(300)             // Vaike-TTL sekundites, kuna API seda ei paku
	dryRun            = os.Getenv("DRY_RUN") == "true" // Kui true, siis ei tehta reaalseid API muudatusi
	debugLog          = os.Getenv("DEBUG") == "true"   // Luba detailsem logimine
)

// --- Zone.eu API Kliendi Struktuurid (Swaggeri põhjal) ---
// fix ing id int type to string
// ZoneBaseRecord on baasstruktuur, mida teised kirjeliigid laiendavad
type ZoneBaseRecord struct {
	ResourceURL string `json:"resource_url,omitempty"` // API URL selle kirje jaoks (readOnly)
	ID          int    `json:"-"`           // Kirje ID (readOnly, kasutatakse kustutamiseks/uuendamiseks)
	Name        string `json:"name"`                   // Kirje FQDN nimi (nt www.minudomeen.com.)
	Delete      bool   `json:"delete,omitempty"`       // Kas kirjet saab kustutada (readOnly)
	Modify      bool   `json:"modify,omitempty"`       // Kas kirjet saab muuta (readOnly)
        IDString    string `json:"id,omitempty"`
}

func (z *ZoneBaseRecord) UnmarshalJSON(data []byte) error {
    type Alias ZoneBaseRecord
    aux := &struct {
        *Alias
    }{
        Alias: (*Alias)(z),
    }
    if err := json.Unmarshal(data, &aux); err != nil {
        return err
    }
    id, err := strconv.Atoi(aux.IDString)
    if err != nil {
        return err
    }
    z.ID = id
    return nil
}
// ZoneARecord esindab A kirjet
type ZoneARecord struct {
	ZoneBaseRecord
	Destination string `json:"destination"` // IPv4 aadress
}

// ZoneCNAMERecord esindab CNAME kirjet
type ZoneCNAMERecord struct {
	ZoneBaseRecord
	Destination string `json:"destination"` // Sihtmärk hostname
}

// ZoneTXTRecord esindab TXT kirjet
type ZoneTXTRecord struct {
	ZoneBaseRecord
	Destination string `json:"destination"` // TXT kirje sisu
}

// --- Zone.eu API Kliendi Funktsioonid (Swaggeri põhjal uuendatud) ---

// zoneAPIRequest teeb üldise päringu Zone.eu API-le
func zoneAPIRequest(ctx context.Context, method, path string, reqBody interface{}, respBody interface{}) (int, error) {
	fullURL := zoneAPIURL + path

	var bodyReader io.Reader
	if reqBody != nil {
		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			return 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
		if debugLog {
			log.Printf("DEBUG: Request Body (%s %s): %s", method, fullURL, string(jsonData))
		}
		bodyReader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	// --- Autentimine (HTTP Basic Auth) ---
	if zoneUsername == "" || zoneAPIPassword == "" {
		return 0, fmt.Errorf("ZONE_USERNAME and ZONE_API_PASSWORD environment variables are required")
	}
	auth := base64.StdEncoding.EncodeToString([]byte(zoneUsername + ":" + zoneAPIPassword))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 20 * time.Second}
	if debugLog {
		log.Printf("DEBUG: Sending request: %s %s", method, fullURL)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to execute request to %s: %w", fullURL, err)
	}
	defer resp.Body.Close()

	// Logi rate limit infot
	if debugLog {
		log.Printf("DEBUG: RateLimit Limit: %s, Remaining: %s", resp.Header.Get("X-Ratelimit-Limit"), resp.Header.Get("X-Ratelimit-Remaining"))
	}

	// Loe vastuse keha vigade logimiseks või eduka vastuse parsimiseks
	responseBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		log.Printf("WARNING: Failed to read response body from %s (status %d): %v", fullURL, resp.StatusCode, readErr)
		// Ära katkesta siin, proovi ikka staatuse koodi kontrollida
	}
	if debugLog && len(responseBytes) > 0 {
		log.Printf("DEBUG: Response Body (%d %s): %s", resp.StatusCode, fullURL, string(responseBytes))
	}


	// Veakontroll staatuse koodi põhjal
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errMsg := fmt.Sprintf("API request failed to %s with status %d", fullURL, resp.StatusCode)
		if statusMsg := resp.Header.Get("X-Status-Message"); statusMsg != "" {
			errMsg += " (" + statusMsg + ")"
		}
		if len(responseBytes) > 0 {
			errMsg += ": " + string(responseBytes)
		}
		return resp.StatusCode, fmt.Errorf(errMsg)
	}

	// Kui oodatakse vastuse keha ja see on olemas, proovi dekodeerida
	if respBody != nil && len(responseBytes) > 0 && resp.StatusCode != http.StatusNoContent {
		if err := json.Unmarshal(responseBytes, respBody); err != nil {
			return resp.StatusCode, fmt.Errorf("failed to decode successful API response from %s: %w (body: %s)", fullURL, err, string(responseBytes))
		}
	}

	return resp.StatusCode, nil
}

// getZoneRecords hangib olemasolevad A, CNAME ja TXT kirjed Zone.eu API-st
func getZoneRecords(ctx context.Context, zoneName string) ([]*endpoint.Endpoint, error) {
	log.Printf("Fetching records for zone: %s", zoneName)
	endpoints := []*endpoint.Endpoint{}
	var wg sync.WaitGroup
	var mu sync.Mutex // Mutex endpoints listi kaitsmiseks
	fetchErrors := make(chan error, 3) // Kanal vigade kogumiseks

	recordTypes := []string{"a", "cname", "txt"}
	wg.Add(len(recordTypes))

	for _, recordType := range recordTypes {
		go func(rtype string) {
			defer wg.Done()
			path := fmt.Sprintf("/dns/%s/%s", zoneName, rtype)
			var records []json.RawMessage // Kasutame RawMessage, et hiljem tüübipõhiselt dekodeerida

			_, err := zoneAPIRequest(ctx, http.MethodGet, path, nil, &records)
			if err != nil {
				// Käsitse 404 kui tühja tulemust, mitte viga
				if httpErr, ok := err.(interface{ StatusCode() int }); ok && httpErr.StatusCode() == http.StatusNotFound {
					log.Printf("INFO: No %s records found for zone %s (404)", strings.ToUpper(rtype), zoneName)
					return // Tsoonis pole seda tüüpi kirjeid, see on OK
				}
				log.Printf("ERROR fetching %s records for zone %s: %v", strings.ToUpper(rtype), zoneName, err)
				fetchErrors <- fmt.Errorf("failed to fetch %s records: %w", strings.ToUpper(rtype), err)
				return
			}

			mu.Lock()
			defer mu.Unlock()

			for _, rawRecord := range records {
				var baseRecord ZoneBaseRecord
				if err := json.Unmarshal(rawRecord, &baseRecord); err != nil {
					log.Printf("ERROR unmarshalling base record (%s) for zone %s: %v, raw: %s", rtype, zoneName, err, string(rawRecord))
					continue
				}

				// Veendu, et nimi lõppeb punktiga
				dnsName := baseRecord.Name
				if !strings.HasSuffix(dnsName, ".") {
					dnsName += "."
				}

				// ExternalDNS eeldab FQDN-i, mis lõppeb punktiga
				// Kontrolli vastavust domeenifiltrile
				if !domainFilter.Match(dnsName) {
					if debugLog {
						log.Printf("DEBUG: Skipping record %s (%s) because it doesn't match domain filter", dnsName, rtype)
					}
					continue
				}

				var target string
				recordTypeUpper := strings.ToUpper(rtype)

				switch recordTypeUpper {
				case "A":
					var rec ZoneARecord
					if err := json.Unmarshal(rawRecord, &rec); err == nil {
						target = rec.Destination
					} else {
						log.Printf("ERROR unmarshalling A record %s: %v", dnsName, err)
						continue
					}
				case "CNAME":
					var rec ZoneCNAMERecord
					if err := json.Unmarshal(rawRecord, &rec); err == nil {
						target = rec.Destination
						// Veendu, et CNAME sihtmärk lõpeb punktiga, kui see on FQDN
						if !strings.HasSuffix(target, ".") && strings.Contains(target, ".") {
							target += "."
						}
					} else {
						log.Printf("ERROR unmarshalling CNAME record %s: %v", dnsName, err)
						continue
					}
				case "TXT":
					var rec ZoneTXTRecord
					if err := json.Unmarshal(rawRecord, &rec); err == nil {
						// ExternalDNS tavaliselt tahab TXT sisu ilma väliste jutumärkideta.
						// API spec ei täpsusta, eeldame, et API tagastab ilma.
						target = rec.Destination
					} else {
						log.Printf("ERROR unmarshalling TXT record %s: %v", dnsName, err)
						continue
					}
				default:
					log.Printf("WARNING: Skipping unsupported record type %s found via API", recordTypeUpper)
					continue
				}

				// PARANDUS: Kasuta otse string tüüpi recordTypeUpper
				ep := endpoint.NewEndpointWithTTL(dnsName, recordTypeUpper, defaultTTL, target)

				// Salvesta Zone.eu kirje ID ja TÜÜP ProviderSpecific property'sse
				ep.ProviderSpecific = append(ep.ProviderSpecific,
					endpoint.ProviderSpecificProperty{Name: "zone-record-id", Value: strconv.Itoa(baseRecord.ID)},
					endpoint.ProviderSpecificProperty{Name: "zone-record-type", Value: recordTypeUpper}, // Salvesta tüüp kustutamiseks
				)

				endpoints = append(endpoints, ep)

			} // end for rawRecord

		}(recordType) // end go func
	} // end for recordTypes

	wg.Wait()
	close(fetchErrors)

	// Kontrolli, kas esines vigu
	var combinedErr error
	for err := range fetchErrors {
		if combinedErr == nil {
			combinedErr = err
		} else {
			combinedErr = fmt.Errorf("%w; %v", combinedErr, err)
		}
	}
	if combinedErr != nil {
		// Ära tagasta viga, kui see oli ainult 404 (pole kirjeid)
		// See vajaks keerukamat veatüüpide kontrolli, hetkel tagastame vea igal juhul
		return nil, combinedErr
	}


	log.Printf("Fetched and converted %d records (A, CNAME, TXT) for zone %s", len(endpoints), zoneName)
	return endpoints, nil
}

// createZoneRecord loob uue kirje Zone.eu API kaudu
func createZoneRecord(ctx context.Context, zoneName string, record *endpoint.Endpoint, target string) error {
	recordTypeLower := strings.ToLower(string(record.RecordType))
	log.Printf("Creating %s record in zone %s: Name=%s, Target=%s", record.RecordType, zoneName, record.DNSName, target)

	if dryRun {
		log.Printf("DRY-RUN: Skipping creation of %s record %s -> %s", record.RecordType, record.DNSName, target)
		return nil
	}

	path := fmt.Sprintf("/dns/%s/%s", zoneName, recordTypeLower)
	var payload interface{}

	// API eeldab FQDN nime
	recordNameFQDN := record.DNSName
	// Veendu, et nimi lõpeb punktiga API jaoks (kuigi spec näitab ilma, testimine näitab, et punktiga töötab paremini)
	if !strings.HasSuffix(recordNameFQDN, ".") {
		recordNameFQDN += "."
	}


	switch record.RecordType {
	case "A":
		payload = ZoneARecord{
			ZoneBaseRecord: ZoneBaseRecord{Name: recordNameFQDN},
			Destination:    target,
		}
	case "CNAME":
         // Veendu, et CNAME sihtmärk lõpeb punktiga, kui see on FQDN
         if !strings.HasSuffix(target, ".") && strings.Contains(target, ".") {
            target += "."
         }
		payload = ZoneCNAMERecord{
			ZoneBaseRecord: ZoneBaseRecord{Name: recordNameFQDN},
			Destination:    target,
		}
	case "TXT":
		// ExternalDNS võib anda TXT sihtmärgi jutumärkidega, API tõenäoliselt tahab ilma.
		// Eeldame, et API tahab sisu ilma väliste jutumärkideta.
		payload = ZoneTXTRecord{
			ZoneBaseRecord: ZoneBaseRecord{Name: recordNameFQDN},
			Destination:    target, // Kasuta otse targetit
		}
	default:
		return fmt.Errorf("unsupported record type for creation: %s", record.RecordType)
	}

	// API vastab loodud kirjega (massiivis), aga me ei vaja seda siin hetkel.
	_, err := zoneAPIRequest(ctx, http.MethodPost, path, payload, nil)
	if err != nil {
		return fmt.Errorf("failed to create %s record %s via Zone API: %w", record.RecordType, record.DNSName, err)
	}

	log.Printf("Successfully created %s record: Name=%s, Target=%s", record.RecordType, record.DNSName, target)
	return nil
}

// deleteZoneRecord kustutab kirje Zone.eu API kaudu, kasutades ID-d ja tüüpi
func deleteZoneRecord(ctx context.Context, zoneName string, recordID string, recordType string) error {
	recordTypeLower := strings.ToLower(recordType)
	log.Printf("Deleting %s record in zone %s: ID=%s", recordType, zoneName, recordID)

	if recordID == "" || recordType == "" {
		return fmt.Errorf("cannot delete record without ID and Type (ID: %s, Type: %s)", recordID, recordType)
	}
	if recordTypeLower != "a" && recordTypeLower != "cname" && recordTypeLower != "txt" {
		return fmt.Errorf("unsupported record type for deletion: %s", recordType)
	}

	if dryRun {
		log.Printf("DRY-RUN: Skipping deletion of %s record ID %s", recordType, recordID)
		return nil
	}

	path := fmt.Sprintf("/dns/%s/%s/%s", zoneName, recordTypeLower, recordID)

	statusCode, err := zoneAPIRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		// API tagastab 404, kui kirje on juba kustutatud - seda võib ignoreerida
		if statusCode == http.StatusNotFound {
			log.Printf("Record ID %s (Type %s) not found in zone %s (already deleted?), ignoring.", recordID, recordType, zoneName)
			return nil
		}
		return fmt.Errorf("failed to delete %s record ID %s via Zone API: %w", recordType, recordID, err)
	}

	log.Printf("Successfully deleted %s record ID: %s", recordType, recordID)
	return nil
}

// --- external-dns Provider Interface Implementation ---

// ZoneProvider implementeerib external-dns provider.Provider liidese
type ZoneProvider struct {
	provider.BaseProvider
	// clientMutex sync.Mutex // Mutex vajadusel, kui API klient pole thread-safe
	domainFilter endpoint.DomainFilter // Domeenifilter
}

// NewZoneProvider loob uue ZoneProvider'i instantsi
func NewZoneProvider(domainFilter endpoint.DomainFilter) *ZoneProvider {
	return &ZoneProvider{
		domainFilter: domainFilter,
	}
}

// Records hangib olemasolevad DNS kirjed ja konverdib need external-dns formaati
func (p *ZoneProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	// p.clientMutex.Lock()
	// defer p.clientMutex.Unlock()

	if len(p.domainFilter.Filters) == 0 {
		return nil, fmt.Errorf("domain filter is not configured (ZONE_DOMAIN_FILTER env var)")
	}

	allEndpoints := []*endpoint.Endpoint{}
	var combinedErr error

	// Käi läbi kõik hallatavad tsoonid (kui neid on mitu)
	processedZones := make(map[string]bool)
	for _, filter := range p.domainFilter.Filters {
        // Eemalda võimalikud alamdomeenid, et saada tsooni nimi
        zoneName := findZoneName(filter, p.domainFilter.Filters)
        if zoneName == "" {
             log.Printf("WARNING: Could not determine zone name for filter '%s', skipping.", filter)
             continue
        }
        if _, processed := processedZones[zoneName]; processed {
             continue // Väldi sama tsooni mitmekordset töötlemist
        }
        processedZones[zoneName] = true

		zoneEndpoints, err := getZoneRecords(ctx, zoneName)
		if err != nil {
			log.Printf("ERROR fetching records for zone %s: %v", zoneName, err)
			if combinedErr == nil {
				combinedErr = fmt.Errorf("zone %s: %w", zoneName, err)
			} else {
				combinedErr = fmt.Errorf("%w; zone %s: %v", combinedErr, zoneName, err)
			}
			// Jätka teiste tsoonidega isegi vea korral
		} else {
			allEndpoints = append(allEndpoints, zoneEndpoints...)
		}
	}

	log.Printf("Total records fetched across all zones: %d", len(allEndpoints))
	return allEndpoints, combinedErr // Tagasta kõik leitud kirjed ja esimene/kombineeritud viga
}

// ApplyChanges rakendab muudatused (loomine, kustutamine)
func (p *ZoneProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	// p.clientMutex.Lock()
	// defer p.clientMutex.Unlock()

	if len(p.domainFilter.Filters) == 0 {
		return fmt.Errorf("domain filter is not configured (ZONE_DOMAIN_FILTER env var)")
	}

	var applyErrors []error

	// Kustutamised
	log.Printf("Processing %d deletions...", len(changes.Delete))
	for _, ep := range changes.Delete {
		zoneName := findZoneName(ep.DNSName, p.domainFilter.Filters)
		if zoneName == "" {
			log.Printf("ERROR: Could not determine zone for deleting %s (%s). Skipping.", ep.DNSName, ep.RecordType)
			applyErrors = append(applyErrors, fmt.Errorf("zone not found for %s", ep.DNSName))
			continue
		}

		log.Printf("Attempting to delete endpoint in zone %s: %s (%s)", zoneName, ep.DNSName, ep.RecordType)
		// PARANDUS: Kasuta abifunktsiooni ProviderSpecific väärtuste leidmiseks
		recordID := findProviderSpecificProperty(ep, "zone-record-id")
		recordType := findProviderSpecificProperty(ep, "zone-record-type")

		if recordID == "" || recordType == "" {
			log.Printf("HOIATUS: Zone.eu record ID or Type not found in ProviderSpecific for deletion target %s (%s). Skipping deletion. Properties: %v", ep.DNSName, ep.RecordType, ep.ProviderSpecific)
			applyErrors = append(applyErrors, fmt.Errorf("missing ID/Type for %s", ep.DNSName))
			continue
		}

		err := deleteZoneRecord(ctx, zoneName, recordID, recordType)
		if err != nil {
			log.Printf("ERROR deleting record ID %s (%s) for %s: %v", recordID, recordType, ep.DNSName, err)
			applyErrors = append(applyErrors, fmt.Errorf("delete %s (%s): %w", ep.DNSName, recordType, err))
			// Ära katkesta, jätka teiste muudatustega
		}
	}

	// Loomised
	log.Printf("Processing %d creations...", len(changes.Create))
	for _, ep := range changes.Create {
        zoneName := findZoneName(ep.DNSName, p.domainFilter.Filters)
		if zoneName == "" {
			log.Printf("ERROR: Could not determine zone for creating %s (%s). Skipping.", ep.DNSName, ep.RecordType)
			applyErrors = append(applyErrors, fmt.Errorf("zone not found for %s", ep.DNSName))
			continue
		}

		log.Printf("Attempting to create endpoint in zone %s: %s (%s) -> %v", zoneName, ep.DNSName, ep.RecordType, ep.Targets)

		// Loome iga sihtmärgi jaoks eraldi kirje.
		for _, target := range ep.Targets {
			if target == "" {
				log.Printf("WARNING: Skipping creation of %s record %s due to empty target.", ep.RecordType, ep.DNSName)
				continue
			}

			// Veendu, et TTL on mõistlik
			if ep.RecordTTL <= 0 {
				ep.RecordTTL = defaultTTL
			}

			err := createZoneRecord(ctx, zoneName, ep, target)
			if err != nil {
				log.Printf("ERROR creating record %s (%s) target %s: %v", ep.DNSName, ep.RecordType, target, err)
				applyErrors = append(applyErrors, fmt.Errorf("create %s (%s): %w", ep.DNSName, ep.RecordType, err))
			}
		}
	}

	// Uuendamised (käsitletakse Delete+Create kaudu)
	if len(changes.UpdateNew) > 0 || len(changes.UpdateOld) > 0 {
		log.Printf("INFO: %d UpdateOld and %d UpdateNew operations are handled by Delete+Create.", len(changes.UpdateOld), len(changes.UpdateNew))
	}

	// Tagasta koondviga
	if len(applyErrors) > 0 {
		log.Printf("Finished applying changes with %d errors.", len(applyErrors))
		var combinedErr error
		for i, err := range applyErrors {
			if i == 0 {
				combinedErr = err
			} else {
				combinedErr = fmt.Errorf("%w; %v", combinedErr, err)
			}
		}
		return combinedErr
	}

	log.Println("Finished applying changes successfully.")
	return nil
}

// --- HTTP Webhook Server ---

// handleRecords töötleb external-dns päringuid
func handleRecords(w http.ResponseWriter, r *http.Request) {
	provider := NewZoneProvider(domainFilter) // Loo uus provider iga päringu jaoks

	switch r.Method {
	case http.MethodGet:
		log.Println("Received GET /records request")
		endpoints, err := provider.Records(r.Context())
		if err != nil {
			log.Printf("Error fetching records: %v", err)
			http.Error(w, fmt.Sprintf("Error fetching records: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(endpoints); err != nil {
			log.Printf("Error encoding records response: %v", err)
		}

	case http.MethodPost:
		log.Println("Received POST /records request")
		var changes plan.Changes
		bodyBytes, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			log.Printf("Error reading request body: %v", readErr)
			http.Error(w, "Error reading request body", http.StatusInternalServerError)
			return
		}
		r.Body.Close()

		if debugLog {
			log.Printf("DEBUG: Received POST body: %s", string(bodyBytes))
		}

		if err := json.Unmarshal(bodyBytes, &changes); err != nil {
			log.Printf("Error decoding request body: %v", err)
			http.Error(w, fmt.Sprintf("Error decoding request body: %v", err), http.StatusBadRequest)
			return
		}

		err := provider.ApplyChanges(r.Context(), &changes)
		if err != nil {
			log.Printf("Error applying changes: %v", err)
			http.Error(w, fmt.Sprintf("Error applying changes: %v", err), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		log.Printf("Unsupported method: %s", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleHealthz lihtne tervisekontrolli endpoint
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "OK")
}

// --- Abifunktsioonid ---

// getEnv hangib keskkonnamuutuja väärtuse või tagastab vaikimisi väärtuse
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// parseDomainFilter töötleb komadega eraldatud domeenifiltrit
func parseDomainFilter(filter string) []string {
	if filter == "" {
		return []string{}
	}
	parts := strings.Split(filter, ",")
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			if !strings.HasSuffix(trimmed, ".") {
				trimmed += "."
			}
			cleaned = append(cleaned, trimmed)
		}
	}
	return cleaned
}

// findZoneName leiab antud FQDN jaoks kõige sobivama tsooni nime hallatavate filtrite hulgast.
// Tagastab tsooni nime ilma lõpupunktita.
func findZoneName(fqdn string, zoneFilters []string) string {
    bestMatch := ""
    fqdn = strings.TrimSuffix(fqdn, ".")

    for _, filter := range zoneFilters {
        zone := strings.TrimSuffix(filter, ".")
        if fqdn == zone || strings.HasSuffix(fqdn, "."+zone) {
            if len(zone) > len(bestMatch) {
                bestMatch = zone
            }
        }
    }
    return bestMatch
}

// findProviderSpecificProperty otsib väärtust endpoint.ProviderSpecific massiivist nime järgi.
func findProviderSpecificProperty(ep *endpoint.Endpoint, name string) string {
	for _, prop := range ep.ProviderSpecific {
		if prop.Name == name {
			return prop.Value
		}
	}
	return ""
}


func main() {
	// Kontrolli nõutud keskkonnamuutujaid
	if zoneUsername == "" || zoneAPIPassword == "" {
		log.Fatal("ZONE_USERNAME and ZONE_API_PASSWORD environment variables are required")
	}
	if len(domainFilter.Filters) == 0 {
		log.Fatal("ZONE_DOMAIN_FILTER environment variable is required (e.g., 'minudomeen.com.' or 'alam.minudomeen.com.,teinedomeen.com.')")
	}
	if dryRun {
		log.Println("INFO: Running in DRY-RUN mode. No changes will be applied to Zone.eu API.")
	}
	if debugLog {
		log.Println("INFO: Debug logging enabled.")
	}

	log.Printf("Starting Zone.eu webhook for external-dns on port %s", listenPort)
	log.Printf("API URL: %s", zoneAPIURL)
	log.Printf("Zone(s) managed: %v", domainFilter.Filters)
	log.Printf("Default TTL: %d", defaultTTL)

	mux := http.NewServeMux()
	mux.HandleFunc("/records", handleRecords) // external-dns põhi-endpoint
	mux.HandleFunc("/", handleHealthz)        // Tervisekontroll
	mux.HandleFunc("/healthz", handleHealthz) // Tervisekontroll (alternatiivne tee)

	server := &http.Server{
		Addr:    listenPort,
		Handler: mux,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Could not listen on %s: %v\n", listenPort, err)
	}
}

