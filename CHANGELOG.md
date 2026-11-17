# Änderungen

## Unveröffentlicht

- Küchenansicht `/kueche.html` zum Einbetten (Home Assistant): Woche mit Tauschen und Neu-Erfinden je Tag, Rezeptsuche mit Filter und „auf Tag legen“, Planen, Chat, Einkaufsliste; hell und dunkel, fürs Handy.
- `POST /api/chat`: Gespräch mit dem Modell über Plan, Pool und Vorrat.
- `POST /api/rezepte/erfinden`: neues Rezept nach Wunsch, auf Wunsch gleich in den Plan.
- `/kueche?k=…` öffnet die Ansicht mit einem aus dem Kennwort abgeleiteten Schlüssel; der Kennwort-Keks gilt jetzt auch bei SSO.

## 1.0.0: 2026-11-16

Erste Version mit fertigen Container-Images auf GitHub Packages (amd64, arm64).

- Go 1.27.4
