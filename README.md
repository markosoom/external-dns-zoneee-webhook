# External-dns-zoneee-webhook

## zoneee webhook võimaldab kubernetese klustri siseselt hallata väliseid dns nimesid.
Hetkel on toetatud A, CNAME ja TXT kirjete haldamine.
Töökeskkonnas ei soovita kasutada, pole piisavalt testitud.
Pole lisetud kubernetese näiteid, pead ise mõtlema.

##Kasutamine
Repositoris on oleman main.go fail, alustuseks tuleb see vastavalt oma arch kompileerima ja ehitama näiteks docker image.

`go mod init external-dns-zoneee-webhook`
`go mod tidy`
`go build -o external-dns-zoneee-webhook .`

##Selgituseks
###Autentimine: 
zoneAPIRequest kasutab HTTP Basic Auth'i, kasutades ZONE_USERNAME ja ZONE_API_PASSWORD keskkonnamuutujaid.

### Andmestruktuurid:
Kirjete (ARecord, CNAMERecord, TXTRecord) struktuur on täpsustatud. Oluline on, et name väli sisaldab täielikult kvalifitseeritud domeeninime (FQDN, nt www.minudomeen.com) ja destination sisaldab vastavat väärtust (IP, sihtmärk-hostname, TXT sisu). API tagastab kirjed alati massiivina ([...]).
Lisatud ZoneBaseRecord, ZoneARecord, ZoneCNAMERecord, ZoneTXTRecord struktuurid vastavalt Swaggeri spetsifikatsioonile.
###Endpointid:
DNS kirjete haldamiseks on spetsiifilised URL-id tüübid  (nt /dns/{zone}/a, /dns/{zone}/cname/{id}).
###ID Kasutamine:
Kustutamiseks ja uuendamiseks kasutatakse (PUT) käsku ja kirje ID-d (identificatorit),

###API Kliendi Funktsioonid:
**getZoneRecords:** Hangib A, CNAME ja TXT kirjed eraldi päringutega ja kombineerib tulemused. Kasutab uusi struktuure ja konverdib need endpoint formaati ning salvestab kirje ID ja tüübi ProviderSpecific väljale.
**createZoneRecord:** Ehitab õige URL-i ja päringu keha vastavalt kirje tüübile (A, CNAME, TXT). Kasutab uusi struktuure.
**deleteZoneRecord: ** Ehitab õige URL-i vastavalt kirje ID-le ja tüübile.
**Nime Haldus:** Kood eeldab, et nii API kui external-dns kasutavad FQDN-e (täielikult kvalifitseeritud domeeninimesid, mis lõpevad punktiga), mis lihtsustab konverteerimist. Lisatud abifunktsioon findZoneName, et leida õige tsoon mitme filtri korral.
**TTL:** Kuna API spetsifikatsioon ei sisalda TTL-i, kasutatakse konfigureeritavat vaikeväärtust defaultTTL (hetkel 300s).
**Konfiguratsioon:** Lisatud ZONE_USERNAME ja muudetud ZONE_API_TOKEN -> ZONE_API_PASSWORD. Lisatud DRY_RUN ja DEBUG lipud. ZONE_DOMAIN_FILTER võib nüüd sisaldada komadega eraldatud domeene.
**Vigade Haldus ja Logimine:** Täiustatud logimist (sh valikuline DEBUG logimine) ja vigade käsitlemist API päringutes ja muudatuste rakendamisel.

##Kasutusjuhised:

###Keskkonnamuutujad:
ZONE_USERNAME: Sinu ZoneID kasutajanimi.
ZONE_API_PASSWORD: Sinu ZoneID API võti/parool.
ZONE_DOMAIN_FILTER: Kohustuslik! Komadega eraldatud nimekiri hallatavatest tsoonidest (nt minudomeen.com.,teinedomeen.org.). Lisa lõppu punkt!
LISTEN_PORT (valikuline): Port, millel rakendus kuulab (vaikimisi :8888).
DRY_RUN (valikuline): Seadista true, et testida ilma reaalsete API muudatusteta.
DEBUG (valikuline): Seadista true detailsemaks logimiseks.

###Kompileerimine ja Käivitamine:
Nagu eelnevalt kirjeldatud (kasuta go build ja käivita binaar koos keskkonnamuutujatega).
###external-dns Konfiguratsioon:
Konfigureeri external-dns kasutama webhook providerit ja viita sellele rakendusele. Veendu, et external-dns --domain-filter argument vastab ZONE_DOMAIN_FILTER muutujale.

###Ehita multiplatvorm Docker image
`
$ docker buildx build --builder=container --platform linux/arm64,linux/amd64 -t markosoom/external-dns-zoneee-webhook . -f Dockerfile --push
`

##Vaja teha.
Lisatud kubernetese näide.
Lisa curl-i näiteid kuidas curl-iga testida.
