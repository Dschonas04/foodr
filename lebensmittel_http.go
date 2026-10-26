package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// ---------- Lebensmittel ----------

func (d *Dienst) lebensmittelSuche(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	grenze, _ := strconv.Atoi(q.Get("grenze"))
	if grenze <= 0 || grenze > 200 {
		grenze = 50
	}
	treffer := d.katalog.Suche(q.Get("q"), q.Get("kategorie"), grenze)
	raus := make([]*LebensmittelKurz, len(treffer))
	for i, lm := range treffer {
		raus[i] = d.katalog.Kurz(lm)
	}
	gib(w, raus)
}

func (d *Dienst) lebensmittelKategorien(w http.ResponseWriter, r *http.Request) {
	anzahl := map[string]int{}
	for _, lm := range d.katalog.Suche("", "", 0) {
		anzahl[lm.Kategorie]++
	}
	type kat struct {
		Code   string `json:"code"`
		Name   string `json:"name"`
		Anzahl int    `json:"anzahl"`
	}
	raus := []kat{}
	for _, k := range Kategorien {
		raus = append(raus, kat{k.Code, k.Name, anzahl[k.Code]})
	}
	gib(w, raus)
}

func (d *Dienst) lebensmittel(w http.ResponseWriter, r *http.Request) {
	lm := d.katalog.Hole(r.PathValue("id"))
	if lm == nil {
		fehler(w, 404, "nicht gefunden")
		return
	}
	gib(w, d.katalog.Voll(lm))
}

func (d *Dienst) naehrstoffe(w http.ResponseWriter, r *http.Request) {
	gib(w, d.katalog.stoffe)
}

func (d *Dienst) lebensmittelSpeichern(w http.ResponseWriter, r *http.Request) {
	var e EigenesLebensmittel
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		fehler(w, 400, "unlesbar: "+err.Error())
		return
	}
	e.ID = 0
	if id := r.PathValue("id"); id != "" {
		n, ok := eigeneNummer(id)
		if !ok {
			fehler(w, 403, "Lebensmittel aus dem BLS lassen sich nicht ändern")
			return
		}
		e.ID = n
	}
	for code, v := range e.Werte {
		if _, bekannt := d.katalog.stelle[code]; !bekannt || v < 0 {
			delete(e.Werte, code)
		}
	}
	id, err := d.ablage.SpeichereLebensmittel(e)
	if err != nil {
		fehler(w, 400, err.Error())
		return
	}
	if err := d.ladeEigene(); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, d.katalog.Voll(d.katalog.Hole(eigeneID(id))))
}

func (d *Dienst) lebensmittelLoeschen(w http.ResponseWriter, r *http.Request) {
	n, ok := eigeneNummer(r.PathValue("id"))
	if !ok {
		fehler(w, 403, "Lebensmittel aus dem BLS lassen sich nicht löschen")
		return
	}
	if err := d.ablage.LoescheLebensmittel(n); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	if err := d.ladeEigene(); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, map[string]any{"ok": true})
}

func eigeneNummer(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, "eigen-")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n > 0
}

func (d *Dienst) ladeEigene() error {
	eigene, err := d.ablage.EigeneLebensmittel()
	if err != nil {
		return err
	}
	d.katalog.SetzeEigene(eigene)
	return nil
}

// ---------- Naehrwerte und Zuordnung ----------

func (d *Dienst) rezeptNaehrwerte(w http.ResponseWriter, r *http.Request) {
	rez, err := d.ablage.Rezept(nummer(r, "id"))
	if err != nil {
		fehler(w, 404, "nicht gefunden")
		return
	}
	vonHand, err := d.ablage.Zuordnungen()
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, d.katalog.Rechne(rez, vonHand))
}

// Je Tag die Werte einer Portion des geplanten Gerichts, dazu der Schnitt
// ueber die bekochten Tage.
func (d *Dienst) planNaehrwerte(w http.ResponseWriter, r *http.Request) {
	tage, err := d.ablage.PlanTage(nummer(r, "id"))
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	vonHand, err := d.ablage.Zuordnungen()
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	type tagWerte struct {
		Tag       string             `json:"tag"`
		RezeptID  int                `json:"rezept_id"`
		JePortion map[string]float64 `json:"je_portion"`
		Erfasst   int                `json:"erfasst"`
		Zutaten   int                `json:"zutaten"`
	}
	raus := struct {
		Tage    []tagWerte         `json:"tage"`
		Schnitt map[string]float64 `json:"schnitt"`
	}{Tage: []tagWerte{}, Schnitt: leereWerte()}
	n := 0
	for _, t := range tage {
		if t.Rezept == nil {
			continue
		}
		rw := d.katalog.Rechne(*t.Rezept, vonHand)
		raus.Tage = append(raus.Tage, tagWerte{t.Tag, t.RezeptID, rw.JePortion, rw.Erfasst, len(rw.Zutaten)})
		for c, v := range rw.JePortion {
			raus.Schnitt[c] += v
		}
		n++
	}
	for c, v := range raus.Schnitt {
		if n > 0 {
			raus.Schnitt[c] = runde(v / float64(n))
		}
	}
	gib(w, raus)
}

func (d *Dienst) zuordnungen(w http.ResponseWriter, r *http.Request) {
	z, err := d.ablage.Zuordnungen()
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	liste := make([]Zuordnung, 0, len(z))
	for _, v := range z {
		liste = append(liste, v)
	}
	gib(w, liste)
}

func (d *Dienst) zuordnungSetzen(w http.ResponseWriter, r *http.Request) {
	var z Zuordnung
	if err := json.NewDecoder(r.Body).Decode(&z); err != nil {
		fehler(w, 400, "unlesbar")
		return
	}
	z.Zutat = falte(z.Zutat)
	if z.Zutat == "" {
		fehler(w, 400, "Zutat fehlt")
		return
	}
	if z.Lebensmittel != Auslassen && d.katalog.Hole(z.Lebensmittel) == nil {
		fehler(w, 400, "unbekanntes Lebensmittel")
		return
	}
	if z.StueckG < 0 {
		z.StueckG = 0
	}
	if err := d.ablage.SetzeZuordnung(z); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, z)
}

func (d *Dienst) zuordnungLoeschen(w http.ResponseWriter, r *http.Request) {
	if err := d.ablage.LoescheZuordnung(falte(r.PathValue("zutat"))); err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, map[string]any{"ok": true})
}
