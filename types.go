// Fail: types.go
package main

// Record on baasstruktuur enamike Zone.ee DNS kirjete jaoks
type Record struct {
	ID           string `json:"id,omitempty"` // Nüüd string
	ResourceURL  string `json:"resource_url,omitempty"`
	Name         string `json:"name"` // FQDN
	Destination  string `json:"destination,omitempty"`
	CanDelete    bool   `json:"delete,omitempty"`
	CanModify    bool   `json:"modify,omitempty"`
	RecordType string `json:"-"` // Sisemiseks kasutuseks
	ZoneName   string `json:"-"` // Sisemiseks kasutuseks
}

// MXRecord Zone.ee API jaoks
type MXRecord struct {
	ID           string `json:"id,omitempty"` // Nüüd string
	ResourceURL  string `json:"resource_url,omitempty"`
	Name         string `json:"name"`
	Destination  string `json:"destination"`
	Priority     int    `json:"priority"`
	CanDelete    bool   `json:"delete,omitempty"`
	CanModify    bool   `json:"modify,omitempty"`
	RecordType string `json:"-"`
	ZoneName   string `json:"-"`
}

// SRVRecord Zone.ee API jaoks
type SRVRecord struct {
	ID           string `json:"id,omitempty"` // Nüüd string
	ResourceURL  string `json:"resource_url,omitempty"`
	Name         string `json:"name"`
	Destination  string `json:"destination"`
	Priority     int    `json:"priority"`
	Weight       int    `json:"weight"`
	Port         int    `json:"port"`
	CanDelete    bool   `json:"delete,omitempty"`
	CanModify    bool   `json:"modify,omitempty"`
	RecordType string `json:"-"`
	ZoneName   string `json:"-"`
}

// Vastuste tüübid (massiivid)
type ZoneARecords []Record
type ZoneCNAMERecords []Record
type ZoneTXTRecords []Record
type ZoneMXRecords []MXRecord
type ZoneSRVRecords []SRVRecord

// Päringute kehad (Payloads)
type CreateRecordPayload struct {
	Name        string `json:"name"`
	Destination string `json:"destination"`
}
type UpdateRecordPayload struct {
	Name        string `json:"name"`
	Destination string `json:"destination"`
}
type CreateMXPayload struct {
	Name        string `json:"name"`
	Destination string `json:"destination"`
	Priority    int    `json:"priority"`
}
type UpdateMXPayload struct {
	Name        string `json:"name"`
	Destination string `json:"destination"`
	Priority    int    `json:"priority"`
}
type CreateSRVPayload struct {
	Name        string `json:"name"`
	Destination string `json:"destination"`
	Priority    int    `json:"priority"`
	Weight      int    `json:"weight"`
	Port        int    `json:"port"`
}
type UpdateSRVPayload struct {
	Name        string `json:"name"`
	Destination string `json:"destination"`
	Priority    int    `json:"priority"`
	Weight      int    `json:"weight"`
	Port        int    `json:"port"`
}
