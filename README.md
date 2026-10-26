# Foodr

Essensplaner mit Lebensmitteldatenbank für den eigenen Haushalt, als
Webanwendung. Rezepte wandern in einen Pool, ein lokales Sprachmodell (Ollama)
wählt daraus die Woche aus, Foodr rechnet Einkaufsliste und Nährwerte. Nichts
davon verlässt das eigene Netz.

* **Wochenplan** – vom Modell gewählt, einzelne Tage tauschen oder selbst
  belegen, abhaken, was gekocht wurde.
* **Einkaufsliste** – auf die Personenzahl hochgerechnet, zusammengefasst,
  nach Regal sortiert, ohne das, was im Vorrat steht.
* **Rezepte** – einfügen und vom Modell einlesen lassen (Text oder Adresse)
  oder selbst tippen.
* **Lebensmitteldatenbank** – 7.140 Lebensmittel mit 138 Nährstoffen je
  100 g aus dem Bundeslebensmittelschlüssel (BLS 4.0), durchsuchbar nach Name
  und Gruppe, dazu eigene Lebensmittel.
* **Nährwerte** – je Portion für jedes Rezept und jeden Tag im Plan. Zutaten
  werden von selbst einem Lebensmittel zugeordnet; was falsch getroffen ist,
  legt man einmal von Hand fest.

## Starten

```bash
cp .env.beispiel .env            # Ollama-Adresse und Modell eintragen
docker compose up -d --build
```

Danach läuft die Oberfläche auf `http://localhost:8300`. Ohne erreichbares
Ollama funktioniert alles außer dem Einlesen von Rezepten; der Wochenplan
entsteht dann aus der eigenen Bewertung (siehe unten).

Zwölf Beispielrezepte liegen in [`beispiele/rezepte.json`](beispiele/rezepte.json):

```bash
python3 -c 'import json; [print(json.dumps(r)) for r in json.load(open("beispiele/rezepte.json"))]' |
  while read -r r; do curl -s -X POST -H 'Content-Type: application/json' -d "$r" localhost:8300/api/rezepte >/dev/null; done
```

## Einstellungen

| Variable | Vorgabe | Wozu |
|----------|---------|------|
| `FOODR_DB` | `postgres://foodr:foodr@db:5432/foodr?sslmode=disable` | Datenbank |
| `OLLAMA_ADRESSE` | `http://localhost:11434` (im Verbund `host.docker.internal`) | Modellrechner |
| `OLLAMA_MODELL` | `qwen2.5:7b-instruct` | Modell |
| `FOODR_AUTOPLAN` | `an` | sonntägliche Planung |
| `FOODR_AUTOPLAN_STUNDE` | `17` | ab wann sonntags |
| `FOODR_KENNWORT` | leer | gesetzt: Anmeldung in der Oberfläche, Schnittstelle braucht Kopfzeile `X-Foodr` |
| `FOODR_PORT` | `8300` | Port am Wirt (nur im Verbund) |
| `PORT` | `8300` | Port des Dienstes |

Ohne Kennwort ist Foodr offen für alle, die die Adresse erreichen – gedacht
für das eigene Netz. Wer es von außen erreichbar macht, setzt ein Kennwort
und stellt TLS davor.

**Zur Modellwahl:** ein 7B-Modell, das vollständig in den Grafikspeicher
passt, antwortet in Sekunden. Ein größeres Modell, das nicht hineinpasst,
rechnet auf der CPU, braucht Minuten und liefert eher schlechtere Antworten.

## Wie geplant wird

Das Modell entscheidet, aber nur innerhalb dessen, was der Dienst erlaubt: es
bekommt die bestbewerteten 70 Rezepte mit Kostform, Aufwand, Dauer und dem
Datum des letzten Males auf dem Tisch, und muss Nummern daraus zurückgeben.
Antworten, die einen Tag doppelt belegen, eine unbekannte Nummer nennen oder
ein Datum erfinden, fliegen heraus; bleibt danach ein Tag offen, fragt der
Dienst ein zweites Mal nach. Was dann noch fehlt, füllt die eigene Bewertung:

* je länger nicht gekocht, desto besser (bis 120 Tage),
* was in den letzten sechs Wochen schon auf einem Plan stand, rutscht nach
  hinten,
* Bewertung des Rezepts, passende Jahreszeit, ein Schuss Zufall,
* montags bis freitags Abzug für alles über 45 Minuten, am Wochenende Zuschlag
  für Aufwendiges.

Sonntags ab 17 Uhr plant der Dienst die kommende Woche von selbst, mit den
Einstellungen des letzten Laufs – es sei denn, es steht schon ein Plan.

## Wie die Nährwerte entstehen

1. **Zuordnung.** Jede Zutat wird einem Lebensmittel zugeordnet: zuerst über
   eine Liste gängiger Zutaten („Ei“ → Hühnerei roh, „Karotte“ → Karotte/Möhre
   roh), dann über die Suche mit dem ganzen Namen, mit dem letzten Wort
   („geriebener Käse“ → Käse) und ohne Mehrzahlendung. Rohe Grundlebensmittel
   gehen vor Fertiggerichten. Eine Zuordnung von Hand gilt für alle Rezepte
   mit dieser Zutat; „nicht mitrechnen“ nimmt eine Zutat ganz heraus.
2. **Menge in Gramm.** g, kg, ml und l direkt (1 ml ≈ 1 g); EL 15 g, TL 5 g,
   Prise 0,4 g, Zehe 4 g, Bund 30 g, Dose 400 g, Packung 250 g; „Stück“ nach
   einer Tabelle üblicher Gewichte (Ei 60 g, Zwiebel 100 g …) oder dem
   Stückgewicht eines eigenen Lebensmittels bzw. einer Zuordnung.
3. **Rechnen.** Werte je 100 g mal Gramm, aufsummiert und durch die
   Portionen geteilt. Zutaten ohne Zuordnung oder ohne umrechenbare Menge
   zählen nicht mit; die Rezeptansicht zeigt, welche das sind.

Das sind Schätzungen für den Alltag, keine Ernährungsberatung.

## Schnittstelle

| Weg | Zweck |
|-----|-------|
| `GET /api/status` | Modell, Erreichbarkeit, Anzahl Rezepte und Lebensmittel |
| `POST /api/anmelden` | `{kennwort}` → Sitzungskeks |
| `GET/POST /api/rezepte`, `GET/PUT/DELETE /api/rezepte/{id}` | Pool |
| `POST /api/rezepte/einlesen` | Freitext oder Adresse → Rezept (ungespeichert) |
| `GET /api/rezepte/{id}/naehrwerte` | Nährwerte gesamt und je Portion, je Zutat mit Zuordnung |
| `GET /api/plan?woche=YYYY-MM-DD` | Plan einer Woche (Montag) |
| `POST /api/plan/erzeugen` | `{woche, personen, tage, wuensche, nur_kost, ausschluss}` |
| `PUT /api/plan/tag/{id}` | `{rezept_id, hinweis, woche}` einen Tag selbst belegen |
| `POST /api/plan/tag/{id}/tauschen` | einen Tag neu würfeln |
| `POST /api/plan/tag/{id}/gekocht` | abhaken, merkt sich das Datum am Rezept |
| `GET /api/plan/{id}/naehrwerte` | je Tag eine Portion, dazu der Schnitt |
| `GET /api/plan/{id}/einkauf`, `…/einkauf.txt` | Einkaufsliste, auch als Text |
| `POST /api/plan/{id}/einkauf` | `{name, menge}` ergänzen |
| `PATCH /api/einkauf/{id}`, `DELETE /api/einkauf/{id}` | abhaken, entfernen |
| `GET/POST /api/vorrat`, `DELETE /api/vorrat/{name}` | was immer im Haus ist |
| `GET/PUT /api/regeln` | gemerkte Planungseinstellungen |
| `GET /api/lebensmittel?q=&kategorie=&grenze=` | Suche, Kernwerte je 100 g |
| `GET /api/lebensmittel/kategorien` | Hauptgruppen mit Anzahl |
| `GET /api/lebensmittel/{id}` | alle Nährstoffe, nach Gruppen |
| `POST /api/lebensmittel`, `PUT/DELETE /api/lebensmittel/eigen-{n}` | eigene Lebensmittel `{name, stueck_g, werte: {CODE: Wert}}` |
| `GET /api/naehrstoffe` | die 138 Nährstoffe mit Code, Einheit und Gruppe |
| `GET/PUT /api/zuordnung`, `DELETE /api/zuordnung/{zutat}` | Zuordnungen von Hand `{zutat, lebensmittel, stueck_g}` |

## Entwickeln

```bash
go test ./...          # Einheiten, Einlesen, Lebensmittel, Nährwerte
go build -o foodr .    # Dienst; braucht PostgreSQL unter FOODR_DB
```

Die Lebensmitteldaten liegen aufbereitet in `daten/` und sind in das
Programm eingebettet. `werkzeug/bls_umwandeln.py` erzeugt sie aus den
Excel-Dateien des BLS neu (ohne weitere Python-Pakete).

## Lizenz und Quellen

Der Code steht unter der [MIT-Lizenz](LICENSE).

Die Nährwertdaten stammen aus dem Bundeslebensmittelschlüssel:
**Max Rubner-Institut (2025): Bundeslebensmittelschlüssel (BLS), Version 4.0 –
Deutsche Nährstoffdatenbank. Karlsruhe. DOI:
[10.25826/Data20251217-134202-0](https://doi.org/10.25826/Data20251217-134202-0)**,
veröffentlicht unter [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).
Foodr enthält die Werte unverändert bis auf das Format: Zahlen auf vier
signifikante Stellen gekürzt, Angaben unter der Nachweis- oder
Bestimmungsgrenze als „Spuren“ zusammengefasst, nicht bestimmte Werte leer.
Siehe [NOTICE](NOTICE).
