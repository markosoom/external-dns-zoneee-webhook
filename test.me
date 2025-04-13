
# Küsi kõiki oma domeeni recordeid
./external-dns-zoneee-webhook
ja siis
curl http:/localhost:8888/records|jq

# Test CNAME loomine

curl -X POST http://localhost:8888/records \
-H "Content-Type: application/json" \
-d '{
  "Create": [
    {
      "DNSName": "test-cname.sinudomeen.ee",
      "Targets": ["sinudomeen.ee"],
      "RecordType": "CNAME",
      "SetIdentifier": ""
    }
  ],
  "UpdateOld": [],
  "UpdateNew": [],
  "Delete": []
}'

Vaata kas CNAME loodi.
curl -u "kasutaja:suva2apitoken" -H 'Content-Type: application/json' https://api.zone.eu/v2/dns/sinudomeen.ee/cname|jq

Kui kirje tekkis siis vaata id ja CNAME test kustutamiseks kasuta
export CNAME_ID="12345"

curl -X POST http://localhost:8888/records \
-H "Content-Type: application/json" \
-d '{
  "Create": [],
  "UpdateOld": [],
  "UpdateNew": [],
  "Delete": [
    {
      "DNSName": "test-cname.sinudomeen.ee",
      "Targets": ["sinudomeen.ee"],
      "RecordType": "CNAME",
      "SetIdentifier": "'"${CNAME_ID}"'"
    }
  ]
}'

