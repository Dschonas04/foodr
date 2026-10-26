package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"
)

// Die Auswahl der Woche. Das Modell entscheidet, aber es entscheidet nur
// innerhalb dessen, was hier erlaubt ist: bekannte Rezept-Nummern, die
// gewuenschten Tage, keine Wiederholung. Alles, was nicht passt, fuellt die
// Bewertung unten auf -- ein Plan kommt also auch zustande, wenn Ollama
// gerade schweigt.

const anweisung = `Du bist der Essensplaner eines Haushalts. Du waehlst aus einer
Liste vorhandener Rezepte aus; du erfindest keine neuen und keine Nummern.
Antworte ausschliesslich mit JSON in dieser Form:
{"tage":[{"tag":"YYYY-MM-DD","rezept_id":7,"hinweis":"kurzer Hinweis"}],
 "begruendung":"zwei Saetze, warum diese Woche so aussieht"}
Regeln:
- Genau ein Rezept pro angefragtem Tag, jedes Rezept hoechstens einmal pro Woche.
- Nur Nummern aus der Liste.
- Montag bis Freitag eher schnelle Gerichte, am Wochenende darf es aufwendig sein.
- Abwechslung: nicht zweimal hintereinander dieselbe Kostform, ueber die Woche
  mindestens zwei fleischlose Tage, wenn die Liste das hergibt.
- Was zuletzt gekocht oder kuerzlich geplant wurde, kommt nach hinten.
- Der Hinweis ist ein vollstaendiger, praktischer Satz: was am Vortag zu tun
  ist, was eingefroren werden kann, was zu Resten passt. Wenn es nichts zu
  sagen gibt, bleibt er leer. Einzelne Woerter wie "zubereiten" sind kein
  Hinweis.
- Deutsche Umlaute ausschreiben, keine Emojis.`

type kiPlan struct {
	Tage []struct {
		Tag      string `json:"tag"`
		RezeptID int    `json:"rezept_id"`
		Hinweis  string `json:"hinweis"`
	} `json:"tage"`
	Begruendung string `json:"begruendung"`
}

// Was das Modell zu sehen bekommt: knapp, damit viele Rezepte hineinpassen.
type kandidat struct {
	ID        int      `json:"id"`
	Titel     string   `json:"titel"`
	Kost      string   `json:"kost"`
	Aufwand   string   `json:"aufwand"`
	DauerMin  int      `json:"dauer_min"`
	Etiketten []string `json:"etiketten,omitempty"`
	Saison    []string `json:"saison,omitempty"`
	Zuletzt   string   `json:"zuletzt,omitempty"`
}

type Planer struct {
	ablage *Ablage
	ki     *KI
}

func (p *Planer) Erzeuge(ctx context.Context, auf Auftrag) (*Plan, string, error) {
	if auf.Personen <= 0 {
		auf.Personen = 2
	}
	tage, err := tageDerWoche(auf)
	if err != nil {
		return nil, "", err
	}
	rezepte, err := p.ablage.Rezepte()
	if err != nil {
		return nil, "", err
	}
	kuerzlich, err := p.ablage.KuerzlichGeplant(6)
	if err != nil {
		return nil, "", err
	}
	moeglich := filtere(rezepte, auf)
	if len(moeglich) == 0 {
		return nil, "", fmt.Errorf("keine passenden Rezepte im Pool — erst welche anlegen")
	}
	if len(moeglich) < len(tage) {
		return nil, "", fmt.Errorf("nur %d passende Rezepte fuer %d Tage — der Pool ist zu klein",
			len(moeglich), len(tage))
	}

	nachID := map[int]Rezept{}
	for _, r := range moeglich {
		nachID[r.ID] = r
	}
	rang := bewerte(moeglich, kuerzlich)
	kandidaten := waehleKandidaten(moeglich, rang, 70)

	plan := Plan{Woche: auf.Woche, Personen: auf.Personen}
	belegt := map[int]bool{}
	gewaehlt := map[string]PlanTag{}
	erlaubt := map[string]bool{}
	for _, t := range tage {
		erlaubt[t] = true
	}

	// Zwei Versuche: beim zweiten erfaehrt das Modell, welche Tage noch offen
	// sind und welche Rezepte schon vergeben. Kleine Modelle vergeben denselben
	// Topf gern zweimal oder erfinden ein Datum.
	var letzterFehler error
	for versuch := 1; versuch <= 2 && len(gewaehlt) < len(tage); versuch++ {
		offen := []string{}
		for _, t := range tage {
			if _, da := gewaehlt[t]; !da {
				offen = append(offen, t)
			}
		}
		antwort, fehler := p.frage(ctx, auf, offen, kandidaten, belegt, versuch)
		if fehler != nil {
			letzterFehler = fehler
			break
		}
		plan.Modell = p.ki.Modell
		if plan.Begruendung == "" {
			plan.Begruendung = strings.TrimSpace(antwort.Begruendung)
		}
		for _, t := range antwort.Tage {
			if !erlaubt[t.Tag] || belegt[t.RezeptID] {
				continue
			}
			if _, da := nachID[t.RezeptID]; !da {
				continue
			}
			if _, schon := gewaehlt[t.Tag]; schon {
				continue
			}
			hinweis := saeubere(t.Hinweis)
			if !tauglicherHinweis(hinweis, nachID[t.RezeptID].Titel) {
				hinweis = ""
			}
			belegt[t.RezeptID] = true
			gewaehlt[t.Tag] = PlanTag{Tag: t.Tag, RezeptID: t.RezeptID, Hinweis: hinweis}
		}
	}

	var meldung string
	switch {
	case letzterFehler != nil && len(gewaehlt) == 0:
		meldung = "Ollama hat nicht geantwortet, der Plan kommt aus der Bewertung: " +
			letzterFehler.Error()
	case len(gewaehlt) < len(tage):
		meldung = fmt.Sprintf("%d von %d Tagen kamen vom Modell, der Rest aus der Bewertung.",
			len(gewaehlt), len(tage))
	}

	// Luecken schliessen: bestbewertetes Rezept, das noch frei ist und zum Tag passt.
	for _, tag := range tage {
		if _, da := gewaehlt[tag]; da {
			continue
		}
		r := naechstbestes(moeglich, rang, belegt, werktag(tag))
		if r == nil {
			return nil, "", fmt.Errorf("zu wenige Rezepte, um %s zu belegen", tag)
		}
		belegt[r.ID] = true
		gewaehlt[tag] = PlanTag{Tag: tag, RezeptID: r.ID, Hinweis: ersatzHinweis(*r)}
	}
	for _, tag := range tage {
		t := gewaehlt[tag]
		if t.Hinweis == "" {
			t.Hinweis = ersatzHinweis(nachID[t.RezeptID])
		}
		plan.Tage = append(plan.Tage, t)
	}
	if plan.Begruendung == "" {
		plan.Begruendung = "Ausgewaehlt nach Abwechslung, Aufwand am Wochentag und dem, was am " +
			"laengsten nicht auf dem Tisch stand."
	}

	fertig, err := p.ablage.SchreibePlan(plan)
	if err != nil {
		return nil, "", err
	}
	if err := p.RechneEinkauf(fertig); err != nil {
		return nil, "", err
	}
	return fertig, meldung, nil
}

func (p *Planer) frage(ctx context.Context, auf Auftrag, tage []string, kandidaten []kandidat,
	belegt map[int]bool, versuch int) (kiPlan, error) {
	var antwort kiPlan
	frei := kandidaten
	if len(belegt) > 0 {
		frei = frei[:0:0]
		for _, k := range kandidaten {
			if !belegt[k.ID] {
				frei = append(frei, k)
			}
		}
	}
	liste, _ := json.Marshal(frei)
	nachtrag := ""
	if versuch > 1 {
		nachtrag = "\n\nDer erste Vorschlag war unvollstaendig. Belege genau die oben " +
			"genannten Tage, jeden nur einmal, und nimm nur Nummern aus der Liste."
	}
	auftrag := fmt.Sprintf(`Haushalt: %d Personen.
Jahreszeit: %s.
Zu planende Tage (genau diese Datumsangaben verwenden):
%s
Wuensche des Haushalts: %s

Rezepte im Pool:
%s%s`, auf.Personen, jahreszeit(time.Now()), tageMitNamen(tage), wunschOder(auf.Wuensche), liste, nachtrag)
	if err := p.ki.FrageJSON(ctx, anweisung, auftrag, &antwort); err != nil {
		return antwort, err
	}
	return antwort, nil
}

// Einen einzelnen Tag neu wuerfeln, ohne den Rest anzutasten.
func (p *Planer) TauscheTag(tagID int, plan *Plan) (*Plan, error) {
	rezepte, err := p.ablage.Rezepte()
	if err != nil {
		return nil, err
	}
	kuerzlich, err := p.ablage.KuerzlichGeplant(6)
	if err != nil {
		return nil, err
	}
	belegt := map[int]bool{}
	var tag string
	for _, t := range plan.Tage {
		if t.ID == tagID {
			tag = t.Tag
			continue
		}
		if t.RezeptID != 0 {
			belegt[t.RezeptID] = true
		}
	}
	if tag == "" {
		return nil, fmt.Errorf("Tag gehoert nicht zu diesem Plan")
	}
	// Auch das alte Rezept ist gesperrt, sonst kommt dasselbe wieder.
	for _, t := range plan.Tage {
		if t.ID == tagID && t.RezeptID != 0 {
			belegt[t.RezeptID] = true
		}
	}
	rang := bewerte(rezepte, kuerzlich)
	r := naechstbestes(rezepte, rang, belegt, werktag(tag))
	if r == nil {
		return nil, fmt.Errorf("kein anderes Rezept uebrig")
	}
	if err := p.ablage.SetzeTag(tagID, r.ID, ersatzHinweis(*r)); err != nil {
		return nil, err
	}
	neu, err := p.ablage.PlanDerWoche(plan.Woche)
	if err != nil {
		return nil, err
	}
	return neu, p.RechneEinkauf(neu)
}

func (p *Planer) RechneEinkauf(plan *Plan) error {
	vorrat, err := p.ablage.Vorrat()
	if err != nil {
		return err
	}
	return p.ablage.ErsetzeEinkauf(plan.ID, Einkaufsliste(plan.Tage, plan.Personen, vorrat))
}

// ---------- Bewertung ohne Modell ----------

// Je hoeher, desto eher darf es auf den Tisch.
func bewerte(rezepte []Rezept, kuerzlich map[int]string) map[int]float64 {
	heute := time.Now()
	rang := map[int]float64{}
	for _, r := range rezepte {
		punkte := 50.0
		if r.ZuletztGekocht != "" {
			if d, err := time.Parse("2006-01-02", r.ZuletztGekocht); err == nil {
				tage := heute.Sub(d).Hours() / 24
				punkte += min(tage, 120) / 2
			}
		} else {
			punkte += 40 // noch nie gekocht: neugierig sein
		}
		if tag, da := kuerzlich[r.ID]; da {
			if d, err := time.Parse("2006-01-02", tag); err == nil {
				abstand := heute.Sub(d).Hours() / 24
				punkte -= max(0, 40-abstand)
			}
		}
		punkte += float64(r.Bewertung) * 6
		if passtZurJahreszeit(r, heute) {
			punkte += 15
		}
		punkte += rand.Float64() * 12 // damit es nicht jede Woche dieselbe Reihenfolge ist
		rang[r.ID] = punkte
	}
	return rang
}

func naechstbestes(rezepte []Rezept, rang map[int]float64, belegt map[int]bool, istWerktag bool) *Rezept {
	var beste *Rezept
	besteP := -1e9
	for i := range rezepte {
		r := rezepte[i]
		if belegt[r.ID] {
			continue
		}
		p := rang[r.ID]
		if istWerktag && r.DauerMin > 45 {
			p -= 25
		}
		if !istWerktag && r.Aufwand == "aufwendig" {
			p += 10
		}
		if p > besteP {
			besteP = p
			beste = &rezepte[i]
		}
	}
	return beste
}

func waehleKandidaten(rezepte []Rezept, rang map[int]float64, hoechstens int) []kandidat {
	sortiert := append([]Rezept{}, rezepte...)
	sort.Slice(sortiert, func(i, j int) bool { return rang[sortiert[i].ID] > rang[sortiert[j].ID] })
	if len(sortiert) > hoechstens {
		sortiert = sortiert[:hoechstens]
	}
	liste := make([]kandidat, 0, len(sortiert))
	for _, r := range sortiert {
		liste = append(liste, kandidat{ID: r.ID, Titel: r.Titel, Kost: r.Kost, Aufwand: r.Aufwand,
			DauerMin: r.DauerMin, Etiketten: r.Etiketten, Saison: r.Saison, Zuletzt: r.ZuletztGekocht})
	}
	return liste
}

func filtere(rezepte []Rezept, auf Auftrag) []Rezept {
	aus := map[int]bool{}
	for _, id := range auf.Ausschluss {
		aus[id] = true
	}
	kost := map[string]bool{}
	for _, k := range auf.NurKost {
		kost[strings.ToLower(k)] = true
	}
	raus := []Rezept{}
	for _, r := range rezepte {
		if aus[r.ID] || r.Art != "hauptgericht" {
			continue
		}
		if len(kost) > 0 && !kost[strings.ToLower(r.Kost)] {
			continue
		}
		raus = append(raus, r)
	}
	return raus
}

// ---------- Kleinigkeiten ----------

var wochentage = []string{"Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag", "Sonntag"}

func tageDerWoche(auf Auftrag) ([]string, error) {
	montag, err := time.Parse("2006-01-02", auf.Woche)
	if err != nil {
		return nil, fmt.Errorf("Woche muss ein Montag im Format 2006-01-02 sein")
	}
	gewuenscht := map[string]bool{}
	for _, t := range auf.Tage {
		gewuenscht[strings.ToLower(strings.TrimSpace(t))] = true
	}
	tage := []string{}
	for i, name := range wochentage {
		if len(gewuenscht) > 0 && !gewuenscht[strings.ToLower(name)] {
			continue
		}
		tage = append(tage, montag.AddDate(0, 0, i).Format("2006-01-02"))
	}
	if len(tage) == 0 {
		return nil, fmt.Errorf("keine Tage ausgewaehlt")
	}
	return tage, nil
}

func tageMitNamen(tage []string) string {
	var b strings.Builder
	for _, t := range tage {
		d, _ := time.Parse("2006-01-02", t)
		b.WriteString(fmt.Sprintf("- %s (%s)\n", t, wochentage[(int(d.Weekday())+6)%7]))
	}
	return b.String()
}

func werktag(tag string) bool {
	d, err := time.Parse("2006-01-02", tag)
	if err != nil {
		return true
	}
	return d.Weekday() != time.Saturday && d.Weekday() != time.Sunday
}

func jahreszeit(t time.Time) string {
	switch t.Month() {
	case time.March, time.April, time.May:
		return "Fruehling"
	case time.June, time.July, time.August:
		return "Sommer"
	case time.September, time.October, time.November:
		return "Herbst"
	default:
		return "Winter"
	}
}

func passtZurJahreszeit(r Rezept, t time.Time) bool {
	if len(r.Saison) == 0 {
		return false
	}
	jetzt := strings.ToLower(jahreszeit(t))
	for _, s := range r.Saison {
		if strings.ToLower(s) == jetzt {
			return true
		}
	}
	return false
}

func ersatzHinweis(r Rezept) string {
	switch {
	case r.Hinweise != "":
		return r.Hinweise
	case r.DauerMin >= 90:
		return fmt.Sprintf("Dauert etwa %d Minuten — frueh anfangen.", r.DauerMin)
	case r.Kost == "fleisch" || r.Kost == "fisch":
		return "Am Vortag aus dem Gefrierfach nehmen, falls eingefroren."
	default:
		return ""
	}
}

func saeubere(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
}

// Kleine Modelle fuellen das Hinweisfeld gern mit "zubereiten" oder "kochen".
// Solche Hinweise sind keine, also fliegen sie heraus und der eigene Hinweis
// des Rezepts nimmt ihren Platz.
var leerformeln = []string{"zubereiten", "kochen", "servieren", "essen", "geniessen",
	"genießen", "einfach", "lecker", "schnell zubereiten", "kochen und einweichen",
	"vorbereiten und einweichen", "nichts", "kein hinweis", "keine"}

func tauglicherHinweis(s, titel string) bool {
	k := strings.ToLower(strings.TrimSpace(s))
	if len([]rune(k)) < 14 || len(strings.Fields(k)) < 3 {
		return false
	}
	for _, f := range leerformeln {
		if k == f {
			return false
		}
	}
	// "Shakshuka in der Pfanne zubereiten" sagt nichts, was nicht im Rezept
	// steht; der eigene Hinweis des Rezepts ist dann mehr wert.
	if strings.HasPrefix(k, strings.ToLower(titel)) || strings.Contains(k, "zubereiten") {
		return false
	}
	return true
}

func wunschOder(s string) string {
	if strings.TrimSpace(s) == "" {
		return "keine besonderen"
	}
	return s
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
