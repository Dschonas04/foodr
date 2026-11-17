# Änderungen

## Unveröffentlicht

- Chat mit Werkzeugen: der Kochassistent liest Plan, Rezepte, Vorrat und Lebensmitteldatenbank selbst nach und handelt (Tag belegen, tauschen, neu erfinden, abhaken, Einkaufsliste und Vorrat ergänzen, Woche planen). Die Küchenansicht zeigt, was er geändert hat, und lädt den Plan neu.
- Anfragen an denkende Modelle (gemma4) laufen ohne Denkphase (`think: false`): rund zehnmal schneller, und JSON-Antworten bleiben nicht mehr leer.
- Anweisungen an das Modell verlangen echte Umlaute; „Umlaute ausschreiben“ wurde als ae/oe verstanden.

- Küchenansicht `/kueche.html` zum Einbetten (Home Assistant): Woche mit Tauschen und Neu-Erfinden je Tag, Rezeptsuche mit Filter und „auf Tag legen“, Planen, Chat, Einkaufsliste; hell und dunkel, fürs Handy.
- Küchenansicht: Reiter „Lebensmittel“ mit Suche in BLS 4.0 und Open Food Facts samt allen Nährstoffen, Link auf das ganze Foodr; „Neu erfinden“ versucht es bei unbrauchbarer Modellantwort ein zweites Mal.
- `POST /api/chat`: Gespräch mit dem Modell über Plan, Pool und Vorrat.
- `POST /api/rezepte/erfinden`: neues Rezept nach Wunsch, auf Wunsch gleich in den Plan.
- `/kueche?k=…` öffnet die Ansicht mit einem aus dem Kennwort abgeleiteten Schlüssel, der als Kopfzeile nur die Wege der Ansicht öffnet; ohne Kekse, damit es eingebettet auch in Firefox und Safari geht.

## 1.0.0: 2026-11-16

Erste Version mit fertigen Container-Images auf GitHub Packages (amd64, arm64).

- Go 1.27.4
