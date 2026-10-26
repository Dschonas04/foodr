package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Dienst struct {
	ablage     *Ablage
	planer     *Planer
	katalog    *Katalog
	kennwort   string
	offAdresse string
}

func (d *Dienst) Wege(web http.Handler) http.Handler {
	m := http.NewServeMux()

	m.HandleFunc("GET /api/status", d.status)
	m.HandleFunc("POST /api/anmelden", d.anmelden)

	m.HandleFunc("GET /api/rezepte", d.geschuetzt(d.rezepte))
	m.HandleFunc("POST /api/rezepte", d.geschuetzt(d.rezeptSpeichern))
	m.HandleFunc("GET /api/rezepte/{id}", d.geschuetzt(d.rezept))
	m.HandleFunc("PUT /api/rezepte/{id}", d.geschuetzt(d.rezeptSpeichern))
	m.HandleFunc("DELETE /api/rezepte/{id}", d.geschuetzt(d.rezeptLoeschen))
	m.HandleFunc("POST /api/rezepte/einlesen", d.geschuetzt(d.rezeptEinlesen))
	m.HandleFunc("GET /api/rezepte/naehrwerte", d.geschuetzt(d.alleRezeptNaehrwerte))
	m.HandleFunc("GET /api/rezepte/{id}/naehrwerte", d.geschuetzt(d.rezeptNaehrwerte))

	m.HandleFunc("GET /api/lebensmittel", d.geschuetzt(d.lebensmittelSuche))
	m.HandleFunc("GET /api/lebensmittel/kategorien", d.geschuetzt(d.lebensmittelKategorien))
	m.HandleFunc("GET /api/lebensmittel/quellen", d.geschuetzt(d.lebensmittelQuellen))
	m.HandleFunc("POST /api/lebensmittel/import", d.geschuetzt(d.lebensmittelImport))
	m.HandleFunc("GET /api/lebensmittel/{id}", d.geschuetzt(d.lebensmittel))
	m.HandleFunc("POST /api/lebensmittel", d.geschuetzt(d.lebensmittelSpeichern))
	m.HandleFunc("PUT /api/lebensmittel/{id}", d.geschuetzt(d.lebensmittelSpeichern))
	m.HandleFunc("DELETE /api/lebensmittel/{id}", d.geschuetzt(d.lebensmittelLoeschen))
	m.HandleFunc("GET /api/naehrstoffe", d.geschuetzt(d.naehrstoffe))
	m.HandleFunc("GET /api/zuordnung", d.geschuetzt(d.zuordnungen))
	m.HandleFunc("PUT /api/zuordnung", d.geschuetzt(d.zuordnungSetzen))
	m.HandleFunc("DELETE /api/zuordnung/{zutat}", d.geschuetzt(d.zuordnungLoeschen))

	m.HandleFunc("GET /api/plan", d.geschuetzt(d.plan))
	m.HandleFunc("POST /api/plan/erzeugen", d.geschuetzt(d.planErzeugen))
	m.HandleFunc("PUT /api/plan/tag/{id}", d.geschuetzt(d.tagSetzen))
	m.HandleFunc("POST /api/plan/tag/{id}/tauschen", d.geschuetzt(d.tagTauschen))
	m.HandleFunc("POST /api/plan/tag/{id}/gekocht", d.geschuetzt(d.tagGekocht))

	m.HandleFunc("GET /api/plan/{id}/naehrwerte", d.geschuetzt(d.planNaehrwerte))
	m.HandleFunc("GET /api/plan/{id}/einkauf", d.geschuetzt(d.einkauf))
	m.HandleFunc("GET /api/plan/{id}/einkauf.txt", d.geschuetzt(d.einkaufText))
	m.HandleFunc("POST /api/plan/{id}/einkauf", d.geschuetzt(d.postenAnlegen))
	m.HandleFunc("PATCH /api/einkauf/{id}", d.geschuetzt(d.postenAbhaken))
	m.HandleFunc("DELETE /api/einkauf/{id}", d.geschuetzt(d.postenLoeschen))

	m.HandleFunc("GET /api/vorrat", d.geschuetzt(d.vorrat))
	m.HandleFunc("POST /api/vorrat", d.geschuetzt(d.vorratAnlegen))
	m.HandleFunc("DELETE /api/vorrat/{name}", d.geschuetzt(d.vorratLoeschen))

	m.HandleFunc("GET /api/regeln", d.geschuetzt(d.regeln))
	m.HandleFunc("PUT /api/regeln", d.geschuetzt(d.regelnSetzen))

	m.Handle("/", web)
	return protokoll(m)
}

// ---------- Rahmen ----------

func protokoll(n http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anfang := time.Now()
		n.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(anfang).Round(time.Millisecond))
		}
	})
}

// Ohne gesetztes Kennwort laeuft der Dienst offen -- so ist er im Heimnetz
// gedacht. Mit Kennwort verlangt jede Schnittstelle das Merkmal.
func (d *Dienst) geschuetzt(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.kennwort == "" || d.erlaubt(r) {
			h(w, r)
			return
		}
		fehler(w, http.StatusUnauthorized, "anmelden")
	}
}

// Skripte schicken das Kennwort in X-Foodr, der Browser einen Keks mit einem
// davon abgeleiteten Merkmal -- nie das Kennwort selbst.
func (d *Dienst) erlaubt(r *http.Request) bool {
	if gleich(r.Header.Get("X-Foodr"), d.kennwort) {
		return true
	}
	if k, err := r.Cookie("foodr"); err == nil && gleich(k.Value, d.merkmal()) {
		return true
	}
	return false
}

func (d *Dienst) merkmal() string {
	m := hmac.New(sha256.New, []byte(d.kennwort))
	m.Write([]byte("foodr-anmeldung"))
	return hex.EncodeToString(m.Sum(nil))
}

func gleich(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (d *Dienst) anmelden(w http.ResponseWriter, r *http.Request) {
	var rumpf struct {
		Kennwort string `json:"kennwort"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rumpf); err != nil {
		fehler(w, http.StatusBadRequest, "unlesbar")
		return
	}
	if d.kennwort == "" || !gleich(rumpf.Kennwort, d.kennwort) {
		time.Sleep(time.Second) // Raten soll muehsam sein
		fehler(w, http.StatusUnauthorized, "falsches Kennwort")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "foodr", Value: d.merkmal(), Path: "/",
		MaxAge: 60 * 60 * 24 * 365, HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
	gib(w, map[string]any{"ok": true})
}

func gib(w http.ResponseWriter, wert any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(wert)
}

func fehler(w http.ResponseWriter, code int, text string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"fehler": text})
}

func nummer(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.PathValue(name))
	return n
}

// ---------- Rezepte ----------

func (d *Dienst) rezepte(w http.ResponseWriter, r *http.Request) {
	liste, err := d.ablage.Rezepte()
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, liste)
}

func (d *Dienst) rezept(w http.ResponseWriter, r *http.Request) {
	rez, err := d.ablage.Rezept(nummer(r, "id"))
	if err != nil {
		fehler(w, 404, "nicht gefunden")
		return
	}
	gib(w, rez)
}

func (d *Dienst) rezeptSpeichern(w http.ResponseWriter, r *http.Request) {
	var rez Rezept
	if err := json.NewDecoder(r.Body).Decode(&rez); err != nil {
		fehler(w, 400, "unlesbar: "+err.Error())
		return
	}
	if id := nummer(r, "id"); id != 0 {
		rez.ID = id
	}
	fertig, err := d.ablage.SpeichereRezept(rez)
	if err != nil {
		fehler(w, 400, err.Error())
		return
	}
	gib(w, fertig)
}

func (d *Dienst) rezeptLoeschen(w http.ResponseWriter, r *http.Request) {
	if err := d.ablage.LoescheRezept(nummer(r, "id")); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, map[string]any{"ok": true})
}

func (d *Dienst) rezeptEinlesen(w http.ResponseWriter, r *http.Request) {
	var rumpf struct {
		Text   string `json:"text"`
		Quelle string `json:"quelle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rumpf); err != nil {
		fehler(w, 400, "unlesbar")
		return
	}
	ctx, abbruch := context.WithTimeout(r.Context(), 5*time.Minute)
	defer abbruch()
	rez, err := d.planer.Einlesen(ctx, rumpf.Text, rumpf.Quelle)
	if err != nil {
		fehler(w, 422, err.Error())
		return
	}
	gib(w, rez)
}

// ---------- Plan ----------

func (d *Dienst) plan(w http.ResponseWriter, r *http.Request) {
	woche := r.URL.Query().Get("woche")
	if woche == "" {
		woche = MontagDerWoche(time.Now())
	}
	p, err := d.ablage.PlanDerWoche(woche)
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	if p == nil {
		gib(w, map[string]any{"woche": woche, "leer": true})
		return
	}
	gib(w, p)
}

func (d *Dienst) planErzeugen(w http.ResponseWriter, r *http.Request) {
	var auf Auftrag
	if err := json.NewDecoder(r.Body).Decode(&auf); err != nil {
		fehler(w, 400, "unlesbar")
		return
	}
	if auf.Woche == "" {
		auf.Woche = MontagDerWoche(time.Now().AddDate(0, 0, 7))
	}
	ctx, abbruch := context.WithTimeout(r.Context(), 8*time.Minute)
	defer abbruch()
	p, meldung, err := d.planer.Erzeuge(ctx, auf)
	if err != nil {
		fehler(w, 422, err.Error())
		return
	}
	d.merkeAuftrag(auf)
	gib(w, map[string]any{"plan": p, "meldung": meldung})
}

// Die letzten Einstellungen sind die Vorgabe fuer den naechsten Lauf, auch
// fuer den, der sonntags von selbst laeuft.
func (d *Dienst) merkeAuftrag(auf Auftrag) {
	_ = d.ablage.SetzeRegel("personen", strconv.Itoa(auf.Personen))
	_ = d.ablage.SetzeRegel("tage", strings.Join(auf.Tage, ","))
	_ = d.ablage.SetzeRegel("wuensche", auf.Wuensche)
	_ = d.ablage.SetzeRegel("nur_kost", strings.Join(auf.NurKost, ","))
}

func (d *Dienst) tagSetzen(w http.ResponseWriter, r *http.Request) {
	var rumpf struct {
		RezeptID int    `json:"rezept_id"`
		Hinweis  string `json:"hinweis"`
		Woche    string `json:"woche"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rumpf); err != nil {
		fehler(w, 400, "unlesbar")
		return
	}
	if err := d.ablage.SetzeTag(nummer(r, "id"), rumpf.RezeptID, rumpf.Hinweis); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	d.gibPlanNeu(w, rumpf.Woche)
}

func (d *Dienst) tagTauschen(w http.ResponseWriter, r *http.Request) {
	var rumpf struct {
		Woche string `json:"woche"`
	}
	_ = json.NewDecoder(r.Body).Decode(&rumpf)
	if rumpf.Woche == "" {
		rumpf.Woche = MontagDerWoche(time.Now())
	}
	p, err := d.ablage.PlanDerWoche(rumpf.Woche)
	if err != nil || p == nil {
		fehler(w, 404, "kein Plan fuer diese Woche")
		return
	}
	neu, err := d.planer.TauscheTag(nummer(r, "id"), p)
	if err != nil {
		fehler(w, 422, err.Error())
		return
	}
	gib(w, neu)
}

func (d *Dienst) tagGekocht(w http.ResponseWriter, r *http.Request) {
	var rumpf struct {
		Gekocht bool   `json:"gekocht"`
		Woche   string `json:"woche"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rumpf); err != nil {
		fehler(w, 400, "unlesbar")
		return
	}
	if err := d.ablage.HakeTagAb(nummer(r, "id"), rumpf.Gekocht); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	d.gibPlanNeu(w, rumpf.Woche)
}

func (d *Dienst) gibPlanNeu(w http.ResponseWriter, woche string) {
	if woche == "" {
		woche = MontagDerWoche(time.Now())
	}
	p, err := d.ablage.PlanDerWoche(woche)
	if err != nil || p == nil {
		fehler(w, 404, "kein Plan")
		return
	}
	if err := d.planer.RechneEinkauf(p); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, p)
}

// ---------- Einkauf ----------

func (d *Dienst) einkauf(w http.ResponseWriter, r *http.Request) {
	liste, err := d.ablage.Einkauf(nummer(r, "id"))
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, liste)
}

func (d *Dienst) einkaufText(w http.ResponseWriter, r *http.Request) {
	liste, err := d.ablage.Einkauf(nummer(r, "id"))
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	var b strings.Builder
	kategorie := ""
	for _, p := range liste {
		if p.Kategorie != kategorie {
			kategorie = p.Kategorie
			b.WriteString("\n" + kategorie + "\n")
		}
		haken := "[ ]"
		if p.Abgehakt {
			haken = "[x]"
		}
		b.WriteString(fmt.Sprintf("%s %s %s\n", haken, mengeText(p), p.Name))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(strings.TrimSpace(b.String()) + "\n"))
}

func mengeText(p Posten) string {
	if p.Menge == 0 {
		return ""
	}
	text := strconv.FormatFloat(p.Menge, 'f', -1, 64)
	text = strings.ReplaceAll(text, ".", ",")
	if p.Einheit == "" {
		return text + "x"
	}
	return text + " " + p.Einheit
}

func (d *Dienst) postenAnlegen(w http.ResponseWriter, r *http.Request) {
	var p Posten
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		fehler(w, 400, "unlesbar")
		return
	}
	p.Einheit = normEinheit(p.Einheit)
	fertig, err := d.ablage.FuegePosten(nummer(r, "id"), p)
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, fertig)
}

func (d *Dienst) postenAbhaken(w http.ResponseWriter, r *http.Request) {
	var rumpf struct {
		Abgehakt bool `json:"abgehakt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rumpf); err != nil {
		fehler(w, 400, "unlesbar")
		return
	}
	if err := d.ablage.HakePostenAb(nummer(r, "id"), rumpf.Abgehakt); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, map[string]any{"ok": true})
}

func (d *Dienst) postenLoeschen(w http.ResponseWriter, r *http.Request) {
	if err := d.ablage.LoeschePosten(nummer(r, "id")); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, map[string]any{"ok": true})
}

// ---------- Vorrat und Regeln ----------

func (d *Dienst) vorrat(w http.ResponseWriter, r *http.Request) {
	liste, err := d.ablage.Vorrat()
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, liste)
}

func (d *Dienst) vorratAnlegen(w http.ResponseWriter, r *http.Request) {
	var rumpf struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rumpf); err != nil || strings.TrimSpace(rumpf.Name) == "" {
		fehler(w, 400, "Name fehlt")
		return
	}
	if err := d.ablage.FuegeVorrat(rumpf.Name); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	d.vorrat(w, r)
}

func (d *Dienst) vorratLoeschen(w http.ResponseWriter, r *http.Request) {
	if err := d.ablage.LoescheVorrat(r.PathValue("name")); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	d.vorrat(w, r)
}

func (d *Dienst) regeln(w http.ResponseWriter, r *http.Request) {
	regeln, err := d.ablage.Regeln()
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, regeln)
}

func (d *Dienst) regelnSetzen(w http.ResponseWriter, r *http.Request) {
	var rumpf map[string]string
	if err := json.NewDecoder(r.Body).Decode(&rumpf); err != nil {
		fehler(w, 400, "unlesbar")
		return
	}
	for k, v := range rumpf {
		if err := d.ablage.SetzeRegel(k, v); err != nil {
			fehler(w, 500, err.Error())
			return
		}
	}
	d.regeln(w, r)
}

func (d *Dienst) status(w http.ResponseWriter, r *http.Request) {
	ctx, abbruch := context.WithTimeout(r.Context(), 5*time.Second)
	defer abbruch()
	da, modelle := d.planer.ki.Erreichbar(ctx)
	gib(w, map[string]any{
		"ollama":         da,
		"ollama_adresse": d.planer.ki.Adresse,
		"modell":         d.planer.ki.Modell,
		"modelle":        modelle,
		"rezepte":        d.ablage.AnzahlRezepte(),
		"lebensmittel":   d.katalog.Anzahl(),
		"kennwort":       d.kennwort != "",
		"angemeldet":     d.kennwort == "" || d.erlaubt(r),
		"woche":          MontagDerWoche(time.Now()),
		"naechste_woche": MontagDerWoche(time.Now().AddDate(0, 0, 7)),
	})
}

func MontagDerWoche(t time.Time) string {
	versatz := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -versatz).Format("2006-01-02")
}
