package main

import (
	"context"
	"fmt"
	"log"
	"strings"
    "strconv" // Vajalik ID konvertimiseks

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	//"sigs.k8s.io/external-dns/provider"
)

type ZoneProvider struct {
	client      *ZoneClient
	domainFilter endpoint.DomainFilter
	dryRun      bool
}

func NewZoneProvider(domainFilter endpoint.DomainFilter, username, apiKey string, dryRun bool) (*ZoneProvider, error) {
	client := NewZoneClient(username, apiKey)
	return &ZoneProvider{
		client:      client,
		domainFilter: domainFilter,
		dryRun:      dryRun,
	}, nil
}

// Records kasutab nüüd GetZoneEndpoints, mis tagastab otse []*endpoint.Endpoint
func (p *ZoneProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	var allEndpoints []*endpoint.Endpoint

	// Käime läbi kõik domeenifiltri poolt lubatud tsoonid
    // See eeldab, et filter sisaldab tsoone, mida hallata.
    // Kui filter on nt `.example.com`, siis see ei tööta hästi.
    // Eeldame, et filter sisaldab täpseid tsoone: `example.com,other.org`
    if !p.domainFilter.IsConfigured() {
         log.Println("WARN: Domain filter is not configured. Cannot determine zones to manage.")
         return nil, fmt.Errorf("domain filter must be configured with specific zones")
    }

    for _, zoneName := range p.domainFilter.Filters {
        log.Printf("INFO: Fetching records for zone %s", zoneName)
        zoneEndpoints, err := p.client.GetZoneEndpoints(ctx, zoneName)
        if err != nil {
            // Logime vea, aga proovime teisi tsoone ka
             log.Printf("ERROR: Failed to get records for zone %s: %v", zoneName, err)
             continue
             // Alternatiiv: tagasta kohe viga
             // return nil, fmt.Errorf("failed to get records for zone %s: %w", zoneName, err)
        }
         log.Printf("INFO: Found %d manageable endpoints in zone %s", len(zoneEndpoints), zoneName)
         allEndpoints = append(allEndpoints, zoneEndpoints...)
    }


    log.Printf("INFO: Returning %d total endpoints matching the filter", len(allEndpoints))
	return allEndpoints, nil
}

// ApplyChanges rakendab muudatused (täiendatud MX/SRV jaoks)
func (p *ZoneProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	if p.dryRun {
		log.Println("INFO: Dry run mode enabled. Skipping actual changes.")
		for _, ep := range changes.Create {
            log.Printf("DRY-RUN: CREATE %s %s %s (Zone: %s)", ep.DNSName, ep.RecordType, ep.Targets, p.getZoneNameFromEndpoint(ep))
        }
        for _, ep := range changes.UpdateNew {
             log.Printf("DRY-RUN: UPDATE %s %s %s (Zone: %s, ID: %s)", ep.DNSName, ep.RecordType, ep.Targets, p.getZoneNameFromEndpoint(ep), ep.SetIdentifier)
        }
        for _, ep := range changes.Delete {
             log.Printf("DRY-RUN: DELETE %s %s (Zone: %s, ID: %s)", ep.DNSName, ep.RecordType, p.getZoneNameFromEndpoint(ep), ep.SetIdentifier)
        }
		return nil
	}

    log.Printf("INFO: Applying changes: Creates=%d, Updates=%d, Deletes=%d", len(changes.Create), len(changes.UpdateNew), len(changes.Delete))
    var applyErrors []error // Kogume vead kokku

	// Loome kirjed
	for _, ep := range changes.Create {
        zoneName := p.getZoneNameFromEndpoint(ep)
        if zoneName == "" {
            msg := fmt.Sprintf("WARN: Could not determine zone name for creating %s %s. Skipping.", ep.RecordType, ep.DNSName)
            log.Println(msg)
            applyErrors = append(applyErrors, fmt.Errorf(msg))
            continue
        }
		log.Printf("INFO: Creating record %s %s %s in zone %s", ep.DNSName, ep.RecordType, ep.Targets, zoneName)

		err := p.client.CreateRecord(ctx, zoneName, ep) // Kasutame uut client meetodit
		if err != nil {
			msg := fmt.Sprintf("ERROR: Failed to create record %s %s: %v", ep.DNSName, ep.RecordType, err)
            log.Println(msg)
            applyErrors = append(applyErrors, fmt.Errorf(msg))
		} else {
            log.Printf("SUCCESS: Created record %s %s", ep.DNSName, ep.RecordType)
        }
	}

	// Uuendame kirjed
	for i, epNew := range changes.UpdateNew {
		_ = changes.UpdateOld[i] // Vana kirje info

        zoneName := p.getZoneNameFromEndpoint(epNew)
         if zoneName == "" {
            msg := fmt.Sprintf("WARN: Could not determine zone name for updating %s %s. Skipping.", epNew.RecordType, epNew.DNSName)
            log.Println(msg)
            applyErrors = append(applyErrors, fmt.Errorf(msg))
            continue
        }

        recordIDStr := epNew.SetIdentifier
        if recordIDStr == "" {
             msg := fmt.Sprintf("ERROR: Missing record ID (SetIdentifier) for updating %s %s. Skipping.", epNew.DNSName, epNew.RecordType)
             log.Println(msg)
             applyErrors = append(applyErrors, fmt.Errorf(msg))
            continue
        }
        recordID, err := strconv.Atoi(recordIDStr) // Kasuta strconv.Atoi
        if err != nil {
             msg := fmt.Sprintf("ERROR: Invalid record ID format '%s' for updating %s %s: %v. Skipping.", recordIDStr, epNew.DNSName, epNew.RecordType, err)
             log.Println(msg)
             applyErrors = append(applyErrors, fmt.Errorf(msg))
             continue
        }

		log.Printf("INFO: Updating record %s %s (ID: %d) in zone %s to target %s", epNew.DNSName, epNew.RecordType, recordID, zoneName, epNew.Targets)

		err = p.client.UpdateRecord(ctx, zoneName, recordID, epNew) // Kasutame uut client meetodit
		if err != nil {
			msg := fmt.Sprintf("ERROR: Failed to update record %s %s (ID: %d): %v", epNew.DNSName, epNew.RecordType, recordID, err)
            log.Println(msg)
            applyErrors = append(applyErrors, fmt.Errorf(msg))
		} else {
             log.Printf("SUCCESS: Updated record %s %s (ID: %d)", epNew.DNSName, epNew.RecordType, recordID)
        }
	}

	// Kustutame kirjed
	for _, ep := range changes.Delete {
        zoneName := p.getZoneNameFromEndpoint(ep)
        if zoneName == "" {
            msg := fmt.Sprintf("WARN: Could not determine zone name for deleting %s %s. Skipping.", ep.RecordType, ep.DNSName)
            log.Println(msg)
            applyErrors = append(applyErrors, fmt.Errorf(msg))
            continue
        }

        recordIDStr := ep.SetIdentifier
        if recordIDStr == "" {
             msg := fmt.Sprintf("ERROR: Missing record ID (SetIdentifier) for deleting %s %s. Skipping.", ep.DNSName, ep.RecordType)
             log.Println(msg)
             applyErrors = append(applyErrors, fmt.Errorf(msg))
            continue
        }
        recordID, err := strconv.Atoi(recordIDStr) // Kasuta strconv.Atoi
        if err != nil {
            msg := fmt.Sprintf("ERROR: Invalid record ID format '%s' for deleting %s %s: %v. Skipping.", recordIDStr, ep.DNSName, ep.RecordType, err)
            log.Println(msg)
            applyErrors = append(applyErrors, fmt.Errorf(msg))
             continue
        }

		log.Printf("INFO: Deleting record %s %s (ID: %d) from zone %s", ep.DNSName, ep.RecordType, recordID, zoneName)
		err = p.client.DeleteRecord(ctx, zoneName, ep.RecordType, recordID) // recordType on juba string
		if err != nil {
            msg := fmt.Sprintf("ERROR: Failed to delete record %s %s (ID: %d): %v", ep.DNSName, ep.RecordType, recordID, err)
			log.Println(msg)
            applyErrors = append(applyErrors, fmt.Errorf(msg))
		} else {
             log.Printf("SUCCESS: Deleted record %s %s (ID: %d)", ep.DNSName, ep.RecordType, recordID)
        }
	}

	// Tagasta koondviga, kui mõni operatsioon ebaõnnestus
    if len(applyErrors) > 0 {
        // Koosta vigadest üks string
        errorMessages := make([]string, len(applyErrors))
        for i, err := range applyErrors {
            errorMessages[i] = err.Error()
        }
        return fmt.Errorf("encountered %d error(s) during apply changes: %s", len(applyErrors), strings.Join(errorMessages, "; "))
    }

	return nil
}

// getZoneNameFromEndpoint (JÄÄB SAMAKS) - Eeldab, et domainFilter sisaldab tsoone.
func (p *ZoneProvider) getZoneNameFromEndpoint(ep *endpoint.Endpoint) string {
    for _, zone := range p.domainFilter.Filters {
        // Eemaldame lõpust punkti, kui see on olemas nii nimes kui tsoonis
        dnsNameTrimmed := strings.TrimSuffix(ep.DNSName, ".")
        zoneTrimmed := strings.TrimSuffix(zone, ".")
        if dnsNameTrimmed == zoneTrimmed || strings.HasSuffix(dnsNameTrimmed, "."+zoneTrimmed) {
            return zone // Tagastame originaal tsooni nime filtrist
        }
    }
    log.Printf("WARN: Could not determine zone for endpoint %s using domain filter %v", ep.DNSName, p.domainFilter.Filters)
    // Kui filter on täpselt üks tsoon, võime selle tagastada fallbackina?
    if len(p.domainFilter.Filters) == 1 {
        log.Printf("DEBUG: Falling back to single zone in filter: %s", p.domainFilter.Filters[0])
        return p.domainFilter.Filters[0]
    }
    return ""
}


// AdjustEndpoints (JÄÄB SAMAKS)
func (p *ZoneProvider) AdjustEndpoints(endpoints []*endpoint.Endpoint) ([]*endpoint.Endpoint, error) {
	return endpoints, nil
}

// GetDomainFilter (JÄÄB SAMAKS)
func (p *ZoneProvider) GetDomainFilter() endpoint.DomainFilter {
    return p.domainFilter
}

