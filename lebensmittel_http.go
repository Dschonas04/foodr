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
	ab, _ := strconv.Atoi(q.Get("ab"))
	liste, gesamt, err := d.katalog.Suche(Frage{Text: q.Get("q"), Quelle: q.Get("quelle"),
		Gruppe: q.Get("gruppe"), DACH: q.Get("dach") == "1", Ordnen: q.Get("ordnen"), Grenze: grenze, Ab: ab})
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	raus := make([]*LebensmittelKurz, len(liste))
	for i, lm := range liste {
		raus[i] = d.katalog.Kurz(lm)
	}
	gib(w, map[string]any{"treffer": raus, "gesamt": gesamt, "mehr": gesamt > ab+len(raus)})
}

func (d *Dienst) lebensmittelKategorien(w http.ResponseWriter, r *http.Request) {
	type kat struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	raus := []kat{}
	for _, k := range Kategorien {
		raus = append(raus, kat{k.Code, k.Name})
	}
	gib(w, raus)
}

func (d *Dienst) lebensmittelQuellen(w http.ResponseWriter, r *http.Request) {
	gib(w, map[string]any{"quellen": d.katalog.Bestaende(), "import": d.katalog.Import.Kopie()})
}

func (d *Dienst) lebensmittelImport(w http.ResponseWriter, r *http.Request) {
	if !d.katalog.StarteOFF(d.offAdresse) {
		fehler(w, 409, "Ein Import läuft schon.")
		return
	}
	d.lebensmittelQuellen(w, r)
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
	var rumpf struct {
		Name    string             `json:"name"`
		Marke   string             `json:"marke"`
		Gruppe  string             `json:"gruppe"`
		StueckG float64            `json:"stueck_g"`
		Werte   map[string]float64 `json:"werte"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rumpf); err != nil {
		fehler(w, 400, "unlesbar: "+err.Error())
		return
	}
	if kategorieName(rumpf.Gruppe) == "" {
		rumpf.Gruppe = ""
	}
	lm := &Lebensmittel{ID: r.PathValue("id"), Name: rumpf.Name, Marke: strings.TrimSpace(rumpf.Marke),
		Gruppe: rumpf.Gruppe, StueckG: rumpf.StueckG, Werte: rumpf.Werte}
	id, err := d.katalog.SpeichereEigenes(lm)
	if err != nil {
		fehler(w, 400, err.Error())
		return
	}
	gib(w, d.katalog.Voll(d.katalog.Hole(id)))
}

func (d *Dienst) lebensmittelLoeschen(w http.ResponseWriter, r *http.Request) {
	if err := d.katalog.LoescheEigenes(r.PathValue("id")); err != nil {
		fehler(w, 403, err.Error())
		return
	}
	gib(w, map[string]any{"ok": true})
}

// ---------- Naehrwerte und Zuordnung ----------

// Je Rezept die Werte einer Portion, fuer die Rezeptliste.
func (d *Dienst) alleRezeptNaehrwerte(w http.ResponseWriter, r *http.Request) {
	rezepte, err := d.ablage.Rezepte()
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	vonHand, err := d.ablage.Zuordnungen()
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	raus := map[int]map[string]float64{}
	for _, rez := range rezepte {
		raus[rez.ID] = d.katalog.Rechne(rez, vonHand).JePortion
	}
	gib(w, raus)
}

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
