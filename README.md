# External-dns-zoneee-webhook

zoneee webhook rakendus on ehitatud https://api.zone.eu/v2 api docki järgi ja võimaldab kubernetese klustrisiseselt hallata väliseid dns nimesid.
Hetkel on toetatud A, CNAME, TXT, MX ja SRV kirjete haldamine.
Töökeskkonnas ei soovita kasutada, pole piisavalt testitud.
Pole lisatud kubernetese näiteid... pead ise mõtlema.

## Kasutamine
Repositoris on oleman main.go fail, alustuseks tuleb see vastavalt oma arch kompileerima ja ehitama näiteks docker image.
```sh
go mod init external-dns-zoneee-webhook
go mod tidy
go build -o external-dns-zoneee-webhook .
```

## Selgituseks
### Autentimine: 
zoneAPIRequest kasutab HTTP Basic Auth'i, kasutades ZONEEE_API_USER ja ZONEEE_API_PASSWORD keskkonnamuutujaid.

näiteks selline env fail:
```sh
export ZONEEE_API_USER="sinu-zoneid-kasutaja"
export ZONEEE_API_KEY="sinu-zone-api-võti"
export ZONEEE_DOMAIN_FILTER="sinudomeen.ee" 
```
Testimisek käivita käsk
```sh
source env
```
ja siis
```sh
./external-dns-zoneee-webhook --listen-addr ":8080" [--dry-run]
```

## Kasutusjuhised:

### Testimine vastu external-dns-zoneee-webhook rakendust

Küsi kõiki oma domeeni recordeid
```sh
curl http:/localhost:8888/records|jq
```
Test CNAME loomine
```sh
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
```
Vaata kas CNAME loodi.
```sh
curl -u "kasutaja:suva2apitoken" -H 'Content-Type: application/json' https://api.zone.eu/v2/dns/sinudomeen.ee/cname|jq
```

Kui kirje tekkis siis vaata id ja CNAME test kustutamiseks kasuta seda ID-d
```sh
export CNAME_ID="12345"
```

```sh
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
```

#### Ehita multiplatvorm Docker image (hilisemaks kasutamiseks)
```sh
$ docker buildx build --builder=container --platform linux/arm64,linux/amd64 -t markosoom/external-dns-zoneee-webhook . -f Dockerfile --push
```

### Vaja teha.
Kubernetese näidete reaalne testimine. Ja helm charti kirjutamine.

