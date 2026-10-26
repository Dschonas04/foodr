// Foodr -- Essensplaner mit Lebensmitteldatenbank fuer den eigenen Haushalt.
//
// Ein Dienst, eine Datenbank, ein Modell im eigenen Netz: die Rezepte liegen
// in PostgreSQL, die Auswahl der Woche trifft Ollama, die Naehrwerte kommen
// aus dem Bundeslebensmittelschluessel. Die Oberflaeche ist eine Webanwendung
// auf derselben Adresse.
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

//go:embed schema.sql
var schema string

//go:embed web
var webDateien embed.FS

func umgebung(name, vorgabe string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return vorgabe
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime)

	datenbank := umgebung("FOODR_DB", "postgres://foodr:foodr@db:5432/foodr?sslmode=disable")
	ablage, err := OeffneAblage(datenbank, schema)
	if err != nil {
		log.Fatalf("Ablage: %v", err)
	}
	katalog, err := LadeKatalog(ablage.db)
	if err != nil {
		log.Fatalf("Lebensmittel: %v", err)
	}

	ki := NeueKI(
		umgebung("OLLAMA_ADRESSE", "http://localhost:11434"),
		umgebung("OLLAMA_MODELL", "qwen2.5:7b-instruct"),
		12*time.Minute, // ein grosses Modell auf einer Karte braucht Zeit
	)
	planer := &Planer{ablage: ablage, ki: ki}
	dienst := &Dienst{ablage: ablage, planer: planer, katalog: katalog, kennwort: os.Getenv("FOODR_KENNWORT")}
	offAdresse := umgebung("FOODR_OFF_ADRESSE", offAdresse)
	if umgebung("FOODR_OFF", "an") == "an" {
		go katalog.HalteOFFAktuell(offAdresse)
	}
	dienst.offAdresse = offAdresse

	web, err := fs.Sub(webDateien, "web")
	if err != nil {
		log.Fatal(err)
	}

	if umgebung("FOODR_AUTOPLAN", "an") == "an" {
		go plantVonSelbst(ablage, planer)
	}

	adresse := ":" + umgebung("PORT", "8300")
	server := &http.Server{
		Addr:              adresse,
		Handler:           dienst.Wege(http.FileServer(http.FS(web))),
		ReadHeaderTimeout: 20 * time.Second,
	}
	log.Printf("Foodr hoert auf %s, Modell %s auf %s, %d Rezepte im Pool, %d Lebensmittel",
		adresse, ki.Modell, ki.Adresse, ablage.AnzahlRezepte(), katalog.Anzahl())
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// Sonntags nachmittags steht der Plan fuer die kommende Woche von selbst da,
// mit den Einstellungen des letzten Laufs. Wer vorher selbst plant, wird
// nicht ueberschrieben.
func plantVonSelbst(ablage *Ablage, planer *Planer) {
	stunde := 17
	if h, err := strconv.Atoi(umgebung("FOODR_AUTOPLAN_STUNDE", "17")); err == nil {
		stunde = h
	}
	for {
		time.Sleep(20 * time.Minute)
		jetzt := time.Now()
		if jetzt.Weekday() != time.Sunday || jetzt.Hour() < stunde {
			continue
		}
		woche := MontagDerWoche(jetzt.AddDate(0, 0, 1))
		vorhanden, err := ablage.PlanDerWoche(woche)
		if err != nil || vorhanden != nil {
			continue
		}
		regeln, _ := ablage.Regeln()
		auf := Auftrag{Woche: woche, Personen: 2, Wuensche: regeln["wuensche"]}
		if p, err := strconv.Atoi(regeln["personen"]); err == nil && p > 0 {
			auf.Personen = p
		}
		if regeln["tage"] != "" {
			auf.Tage = strings.Split(regeln["tage"], ",")
		}
		if regeln["nur_kost"] != "" {
			auf.NurKost = strings.Split(regeln["nur_kost"], ",")
		}
		ctx, abbruch := context.WithTimeout(context.Background(), 15*time.Minute)
		if _, meldung, err := planer.Erzeuge(ctx, auf); err != nil {
			log.Printf("Wochenplan von selbst: %v", err)
		} else {
			log.Printf("Wochenplan fuer %s von selbst erzeugt. %s", woche, meldung)
		}
		abbruch()
	}
}
