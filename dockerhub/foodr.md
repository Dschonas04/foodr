# Foodr

Essensplaner mit Lebensmitteldatenbank für den eigenen Haushalt, als Webanwendung. Rezepte landen in einem Pool, ein lokales Sprachmodell (Ollama) wählt daraus die Woche aus, Foodr rechnet Einkaufsliste und Nährwerte. Nichts davon verlässt das eigene Netz.

Quellcode, Anleitung und Changelog: [https://github.com/Dschonas04/foodr](https://github.com/Dschonas04/foodr)

## Start

Foodr braucht PostgreSQL. Am einfachsten mit der `docker-compose.yml` aus dem Repository und `image: dschohnas/foodr:1` statt `build: .`:

```yaml
services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: foodr
      POSTGRES_USER: foodr
      POSTGRES_PASSWORD: foodr
    volumes: [foodr_db:/var/lib/postgresql/data]
  dienst:
    image: dschohnas/foodr:1
    depends_on: [db]
    environment:
      FOODR_DB: postgres://foodr:foodr@db:5432/foodr?sslmode=disable
      OLLAMA_ADRESSE: http://host.docker.internal:11434
      OLLAMA_MODELL: qwen2.5:7b-instruct
    extra_hosts: ["host.docker.internal:host-gateway"]
    volumes: [foodr_einstellungen:/einstellungen]
    ports: ["8300:8300"]
volumes:
  foodr_db:
  foodr_einstellungen:
```

Danach läuft Foodr unter `http://<host>:8300`. Die Lebensmitteldaten lädt es beim ersten Start selbst.

## Tags

`latest`, die genaue Version (`1.0.0`) sowie `1.0` und `1`. Jedes Image gibt es für `linux/amd64` und `linux/arm64`. Dieselben Images liegen auch unter `ghcr.io/dschonas04/foodr`.

## Lizenz

Code unter MIT. Nährwertdaten: Max Rubner-Institut, Bundeslebensmittelschlüssel 4.0 (CC BY 4.0). Markenprodukte: Open Food Facts (ODbL), werden zur Laufzeit geladen.
