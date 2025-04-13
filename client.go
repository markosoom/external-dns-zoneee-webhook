// Fail: client.go
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
	"strings"
	"time"

	"sigs.k8s.io/external-dns/endpoint" // Vajalik endpoint.Endpoint jaoks
)

const (
	zoneAPIURL = "https://api.zone.eu/v2"
)

// ZoneClient struct haldab API ühendust ja autentimist
type ZoneClient struct {
	httpClient *http.Client
	username   string
	apiKey     string
}

// NewZoneClient loob uue Zone API kliendi instantsi
func NewZoneClient(username, apiKey string) *ZoneClient {
	return &ZoneClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		username:   username,
		apiKey:     apiKey,
	}
}

// basicAuth genereerib HTTP Basic Auth päise väärtuse
func (c *ZoneClient) basicAuth() string {
	auth := c.username + ":" + c.apiKey
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
}

// doRequest on üldine abifunktsioon API päringute tegemiseks
func (c *ZoneClient) doRequest(ctx context.Context, method, path string, requestBody interface{}, responseTarget interface{}) error {
	url := zoneAPIURL + path

	var reqBodyReader io.Reader
	if requestBody != nil {
		jsonData, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBodyReader = bytes.NewBuffer(jsonData)
		// Logime päringu keha ainult siis, kui see pole tühi (väldime tundliku info logimist GET päringutes)
		// Parandus: Logime alati, aga maskeerime tundliku info vajadusel hiljem. Praegu logime.
		log.Printf("DEBUG: Request Body (%s %s): %s", method, url, string(jsonData))
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", c.basicAuth())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	log.Printf("INFO: Making API request: %s %s", method, url)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	log.Printf("DEBUG: Response Status (%s %s): %s", method, url, resp.Status)
	log.Printf("DEBUG: Response Body (%s %s): %s", method, url, string(respBodyBytes))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("api request failed with status %s: %s", resp.Status, string(respBodyBytes))
	}

	if responseTarget != nil && len(respBodyBytes) > 0 && resp.StatusCode != http.StatusNoContent {
		if err := json.Unmarshal(respBodyBytes, responseTarget); err != nil {
			return fmt.Errorf("failed to unmarshal response body into target type %T: %w. Body: %s", responseTarget, err, string(respBodyBytes))
		}
	}

	return nil
}

// --- Spetsiifilised API meetodid ---

// GetZoneEndpoints hangib KÕIK hallatavad kirjed (A, CNAME, TXT, MX, SRV) tsoonist
// ja konverdib need otse endpoint.Endpoint objektideks.
func (c *ZoneClient) GetZoneEndpoints(ctx context.Context, zoneName string) ([]*endpoint.Endpoint, error) {
	var endpoints []*endpoint.Endpoint
	recordTypes := []string{"a", "cname", "txt", "mx", "srv"} // Hallatavad tüübid

	for _, rt := range recordTypes {
		path := fmt.Sprintf("/dns/%s/%s", zoneName, strings.ToLower(rt))
		log.Printf("INFO: Fetching %s records from %s", strings.ToUpper(rt), path)

		switch rt {
		case "a":
			var records ZoneARecords
			if err := c.doRequest(ctx, http.MethodGet, path, nil, &records); err != nil {
				log.Printf("WARN: Failed to get A records for zone %s: %v", zoneName, err)
				continue // Jätka teiste tüüpidega
			}
			for _, r := range records {
				// TTL on 0, kuna API seda ei halda
				ep := endpoint.NewEndpointWithTTL(r.Name, rt, endpoint.TTL(0), r.Destination)
				ep.SetIdentifier = r.ID // ID on nüüd string
				endpoints = append(endpoints, ep)
			}
		case "cname":
			var records ZoneCNAMERecords
			if err := c.doRequest(ctx, http.MethodGet, path, nil, &records); err != nil {
				log.Printf("WARN: Failed to get CNAME records for zone %s: %v", zoneName, err)
				continue
			}
			for _, r := range records {
				ep := endpoint.NewEndpointWithTTL(r.Name, rt, endpoint.TTL(0), r.Destination)
				ep.SetIdentifier = r.ID // ID on nüüd string
				endpoints = append(endpoints, ep)
			}
		case "txt":
			var records ZoneTXTRecords
			if err := c.doRequest(ctx, http.MethodGet, path, nil, &records); err != nil {
				log.Printf("WARN: Failed to get TXT records for zone %s: %v", zoneName, err)
				continue
			}
			for _, r := range records {
				// TXT kirje sihtmärk (destination) on API vastuses ilma jutumärkideta.
				// External-DNS võib neid oodata, aga standardne käitumine on ilma.
				// Jätame siin ilma ja vajadusel kohandame provideris.
				destination := r.Destination
				ep := endpoint.NewEndpointWithTTL(r.Name, rt, endpoint.TTL(0), destination)
				ep.SetIdentifier = r.ID // ID on nüüd string
				endpoints = append(endpoints, ep)
			}
		case "mx":
			var records ZoneMXRecords
			if err := c.doRequest(ctx, http.MethodGet, path, nil, &records); err != nil {
				log.Printf("WARN: Failed to get MX records for zone %s: %v", zoneName, err)
				continue
			}
			for _, r := range records {
				// Formaat external-dns jaoks: "priority destination"
				target := fmt.Sprintf("%d %s", r.Priority, r.Destination)
				ep := endpoint.NewEndpointWithTTL(r.Name, rt, endpoint.TTL(0), target)
				ep.SetIdentifier = r.ID // ID on nüüd string
				endpoints = append(endpoints, ep)
			}
		case "srv":
			var records ZoneSRVRecords
			if err := c.doRequest(ctx, http.MethodGet, path, nil, &records); err != nil {
				log.Printf("WARN: Failed to get SRV records for zone %s: %v", zoneName, err)
				continue
			}
			for _, r := range records {
				// Formaat external-dns jaoks: "priority weight port destination"
				target := fmt.Sprintf("%d %d %d %s", r.Priority, r.Weight, r.Port, r.Destination)
				ep := endpoint.NewEndpointWithTTL(r.Name, rt, endpoint.TTL(0), target)
				ep.SetIdentifier = r.ID // ID on nüüd string
				endpoints = append(endpoints, ep)
			}
		default:
			log.Printf("WARN: Unsupported record type %s requested in GetZoneEndpoints", rt)
		}
	}
	log.Printf("INFO: Finished fetching records for zone %s, found %d endpoints.", zoneName, len(endpoints))
	return endpoints, nil
}

// CreateRecord loob uue kirje
func (c *ZoneClient) CreateRecord(ctx context.Context, zoneName string, ep *endpoint.Endpoint) error {
	recordType := strings.ToLower(ep.RecordType)
	path := fmt.Sprintf("/dns/%s/%s", zoneName, recordType)
	var payload interface{}
	var responseTarget interface{} // Vajalik vastuse valideerimiseks
	var err error

	// Eeldame ühte sihtmärki Zone API piirangute tõttu
	if len(ep.Targets) != 1 {
		return fmt.Errorf("expected exactly one target for creating record %s %s, got %d", ep.DNSName, ep.RecordType, len(ep.Targets))
	}
	target := ep.Targets[0]

	switch recordType {
	case "a", "cname":
		p := CreateRecordPayload{Name: ep.DNSName, Destination: target}
		payload = p
		responseTarget = &ZoneARecords{} // Ootame massiivi tagasi
	case "txt":
		// Kui external-dns lisab jutumärgid, võtame need siin ära, kui API neid ei taha
		// destination := strings.Trim(target, "\"")
		destination := target // Eeldame, et API ei taha jutumärke
		p := CreateRecordPayload{Name: ep.DNSName, Destination: destination}
		payload = p
		responseTarget = &ZoneTXTRecords{}
	case "mx":
		var prio int
		var dest string
		_, err = fmt.Sscan(target, &prio, &dest)
		if err != nil {
			return fmt.Errorf("failed to parse MX target '%s': %w", target, err)
		}
		p := CreateMXPayload{Name: ep.DNSName, Destination: dest, Priority: prio}
		payload = p
		responseTarget = &ZoneMXRecords{}
	case "srv":
		var prio, weight, port int
		var dest string
		_, err = fmt.Sscan(target, &prio, &weight, &port, &dest)
		if err != nil {
			return fmt.Errorf("failed to parse SRV target '%s': %w", target, err)
		}
		p := CreateSRVPayload{Name: ep.DNSName, Destination: dest, Priority: prio, Weight: weight, Port: port}
		payload = p
		responseTarget = &ZoneSRVRecords{}
	default:
		return fmt.Errorf("unsupported record type for creation: %s", ep.RecordType)
	}

	// Teeme päringu ja proovime vastust töödelda
	err = c.doRequest(ctx, http.MethodPost, path, payload, responseTarget)
	if err != nil {
		// Viga võis tulla nii API päringust kui ka vastuse Unmarshalist
		return fmt.Errorf("failed during create %s record API call or response processing for %s in zone %s: %w", ep.RecordType, ep.DNSName, zoneName, err)
	}
	// Kui viga ei tekkinud, on kõik korras
	return nil
}

// UpdateRecord uuendab olemasolevat kirjet ID järgi
// recordID on int, kuna see tuleb SetIdentifierist (string), mis teisendatakse int-iks provideris
func (c *ZoneClient) UpdateRecord(ctx context.Context, zoneName string, recordID int, ep *endpoint.Endpoint) error {
	recordType := strings.ToLower(ep.RecordType)
	// API path ootab ID-d numbrina (või stringina, mis on number)
	path := fmt.Sprintf("/dns/%s/%s/%d", zoneName, recordType, recordID)
	var payload interface{}
	var responseTarget interface{} // Vajalik vastuse valideerimiseks
	var err error

	// Eeldame ühte sihtmärki
	if len(ep.Targets) != 1 {
		return fmt.Errorf("expected exactly one target for updating record %s %s (ID: %d), got %d", ep.DNSName, ep.RecordType, recordID, len(ep.Targets))
	}
	target := ep.Targets[0]

	switch recordType {
	case "a", "cname":
		p := UpdateRecordPayload{Name: ep.DNSName, Destination: target}
		payload = p
		responseTarget = &ZoneARecords{}
	case "txt":
		// destination := strings.Trim(target, "\"")
		destination := target
		p := UpdateRecordPayload{Name: ep.DNSName, Destination: destination}
		payload = p
		responseTarget = &ZoneTXTRecords{}
	case "mx":
		var prio int
		var dest string
		_, err = fmt.Sscan(target, &prio, &dest)
		if err != nil {
			return fmt.Errorf("failed to parse MX target '%s' for update: %w", target, err)
		}
		p := UpdateMXPayload{Name: ep.DNSName, Destination: dest, Priority: prio}
		payload = p
		responseTarget = &ZoneMXRecords{}
	case "srv":
		var prio, weight, port int
		var dest string
		_, err = fmt.Sscan(target, &prio, &weight, &port, &dest)
		if err != nil {
			return fmt.Errorf("failed to parse SRV target '%s' for update: %w", target, err)
		}
		p := UpdateSRVPayload{Name: ep.DNSName, Destination: dest, Priority: prio, Weight: weight, Port: port}
		payload = p
		responseTarget = &ZoneSRVRecords{}
	default:
		return fmt.Errorf("unsupported record type for update: %s", ep.RecordType)
	}

	// PUT päring tagastab 200 OK
	err = c.doRequest(ctx, http.MethodPut, path, payload, responseTarget)
	if err != nil {
		return fmt.Errorf("failed during update %s record API call or response processing for ID %d in zone %s: %w", ep.RecordType, recordID, zoneName, err)
	}
	return nil
}

// DeleteRecord kustutab kirje ID järgi
// recordID on int, kuna see tuleb SetIdentifierist (string), mis teisendatakse int-iks provideris
func (c *ZoneClient) DeleteRecord(ctx context.Context, zoneName, recordType string, recordID int) error {
	// API path ootab ID-d numbrina (või stringina, mis on number)
	path := fmt.Sprintf("/dns/%s/%s/%d", zoneName, strings.ToLower(recordType), recordID)
	// DELETE päring ei tagasta keha, seega responseTarget on nil
	err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to delete %s record %d in zone %s: %w", recordType, recordID, zoneName, err)
	}
	return nil
}
