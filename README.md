# FC-EDDN

FC-EDDN liest den persönlichen Fleet Carrier in Elite Dangerous Live aus der offiziellen Frontier Companion API (CAPI). Der Dienst läuft nach der Anmeldung als CLI oder im Container und sendet freigegebene Daten über das EDDN-Gateway an dessen Abonnenten.

| Daten | Quelle | EDDN |
| --- | --- | --- |
| Öffentliche Rohstoffangebote und Nachfrage | Markt des eigenen Carriers aus `/fleetcarrier` | `commodity/3` |
| Aktuelles Sternsystem und Galaxiekoordinaten | `/fleetcarrier` und ein passendes Originalereignis aus `/journal` | `journal/1` |

Der Commodity-Markt wird direkt am eigenen Carrier abgefragt. Vor dem Upload müssen das Frontier-Profil und das aktuelle Spieljournal bestätigen, dass der Commander am eigenen Carrier angedockt ist. Der Abgleich erfasst Markt-ID, Callsign, Stationstyp und Sternsystem. Der Warenmarkt muss als verfügbar gemeldet sein. Ein explizit leerer Markt wird als leere Liste übertragen. Der Markt-Zeitstempel gibt den Zeitpunkt der Abfrage an. Für die Positionsmeldung müssen Markt-ID, Callsign und Sternsystem mit einem aktuellen Live-Journalereignis dieses Commanders übereinstimmen. Erscheint ein im Hintergrund ausgeführter Sprung ohne passendes Journalereignis, kann das Programm das Sternsystem aus CAPI anzeigen. Die Positionsmeldung folgt, sobald CAPI ein passendes Ereignis bereitstellt, etwa beim Andocken im neuen System. Die Software liest dafür das Frontier-Journal des laufenden UTC-Tags. Einträge aus Legacy, private Carrier-Konten, Guthaben und Inventare werden nicht veröffentlicht.

## Erster Start

1. Registriere einen eigenen OAuth-PKCE-Client im [Frontier Developer Portal](https://user.frontierstore.net/developer/) und lasse den Zugriff auf den Scope `capi` freischalten. Hinterlege `http://127.0.0.1:38473/callback` als exakte Redirect-URI oder setze unten die abweichende lokale URI.
2. Baue das Programm mit `go build .` oder lade das Binary für dein System aus dem GitHub Release herunter. Benenne das Binary gegebenenfalls in `fc-eddn` bzw. `fc-eddn.exe` um.
3. Melde dich im Browser mit dem Frontier-Konto des Carrier-Eigentümers an. Öffne dazu die URL, die `login` anzeigt. Der Browser und das CLI müssen beim Login auf demselben Rechner laufen.

Beispiel für PowerShell:

```powershell
$env:FC_EDDN_CLIENT_ID = '<Frontier-Client-ID>'
$env:FC_EDDN_COMMANDER = 'Rindula'
.\fc-eddn.exe login
.\fc-eddn.exe sync --once
$env:FC_EDDN_MODE = 'test'
.\fc-eddn.exe sync --once
$env:FC_EDDN_MODE = 'live'
.\fc-eddn.exe sync
```

Unter Linux und macOS setzt du dieselben Variablen mit `export` und startest `./fc-eddn`.

| Variable | Vorgabe | Bedeutung |
| --- | --- | --- |
| `FC_EDDN_CLIENT_ID` | erforderlich | Freigeschaltete Frontier-Client-ID |
| `FC_EDDN_COMMANDER` | erforderlich für Sync | Spielername für den Journalabgleich und die EDDN-Uploader-ID |
| `FC_EDDN_REDIRECT_URI` | `http://127.0.0.1:38473/callback` | Registrierter Loopback-Callback |
| `FC_EDDN_AUDIENCE` | `frontier` | Frontier-OAuth-Audience |
| `FC_EDDN_MODE` | `dry-run` | `dry-run` protokolliert lokal, `test` verwendet EDDN-Testschemas, `live` veröffentlicht |
| `FC_EDDN_INTERVAL` | `15m` | Pause nach einem Sync-Durchlauf, mindestens 15 Minuten |
| `FC_EDDN_STATE_DIR` | `.fc-eddn` | Verzeichnis für OAuth-Tokens und Status der letzten Meldungen |

Der Dienst erneuert Access-Tokens automatisch. Läuft die Frontier-Autorisierung ab oder wird sie widerrufen, führe `login` erneut aus. Stoppe dafür den laufenden Sync. Betreibe pro Carrier und Zustandsverzeichnis eine Instanz, damit sich Token-Erneuerungen und Upload-Status nicht überschneiden. Der Zustand enthält Zugangsdaten und gehört in ein privates Verzeichnis bzw. ein entsprechend geschütztes Volume.

## Docker oder Kubernetes

Führe den Browser-Login lokal aus und übertrage dessen Zustandsverzeichnis auf den Containerhost. Der Container läuft als UID/GID `65532` und benötigt Schreibzugriff auf das Zustandsverzeichnis. `fc.env` enthält die Werte für Client-ID, Commander und Modus. Verwende ein Image aus einem veröffentlichten Release oder baue es selbst mit `docker build -t fc-eddn .`.

```sh
docker run -d --name fc-eddn --restart unless-stopped \
  --env-file fc.env -v "$PWD/.fc-eddn:/state" ghcr.io/rindula/fc-eddn:v0.1.0
```

In Kubernetes kann dasselbe Image als Deployment mit einer Replik, Strategie `Recreate`, UID/GID `65532` und beschreibbarem PersistentVolumeClaim unter `/state` laufen. Dazu gegebenenfalls `fsGroup: 65532` setzen und ein `imagePullSecret` für ein privates GHCR-Paket angeben.

## Schnittstellen

- [Frontier Companion API (EDCD)](https://github.com/EDCD/FDevIDs/tree/master/Frontier%20API)
- [EDDN-Richtlinien und Schemas](https://github.com/EDCD/EDDN/blob/live/docs/Developers.md)
