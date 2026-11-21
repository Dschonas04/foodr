package main

// Die Kuechenansicht (web/kueche.html): Foodr kompakt, etwa eingebettet in
// Home Assistant. Dazu zwei Dinge, die es vorher nicht gab: ein Gespraech mit
// dem Modell ueber Woche und Rezepte, und ein neues Rezept auf Zuruf, wenn im
// Pool nichts Passendes mehr liegt.
//
// Einbetten heisst: keine Anmeldeseite im Rahmen (Keycloak laesst sich nicht
// einbetten). Die Ansicht wird mit einem Schluessel geoeffnet, /kueche?k=...,
// abgeleitet aus dem Kennwort (ein neues Kennwort macht ihn ungueltig). Die
// Seite schickt ihn als Kopfzeile, und er oeffnet nur die Wege der Ansicht.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// kuechenSchluessel ist aus dem Kennwort abgeleitet, nie das Kennwort selbst.
func (d *Dienst) kuechenSchluessel() string {
	if d.kennwort == "" {
		return ""
	}
	m := hmac.New(sha256.New, []byte(d.kennwort))
	m.Write([]byte("foodr-kueche"))
	return hex.EncodeToString(m.Sum(nil))
}

func (d *Dienst) kuechenSchluesselPasst(r *http.Request) bool {
	s := d.kuechenSchluessel()
	if s == "" {
		return false
	}
	// Die Ansicht schickt den Schluessel bei jedem Aufruf in einer Kopfzeile:
	// eingebettet blockieren Browser (Firefox, Safari) den Keks oft. Er oeffnet
	// nur die Wege, die die Ansicht braucht, nie Einstellungen oder SSO.
	if gleich(r.Header.Get("X-Foodr-Kueche"), s) {
		return kuechenWeg(r)
	}
	return r.URL.Path == "/kueche" && gleich(r.URL.Query().Get("k"), s)
}

// kuechenWeg: was die Kuechenansicht aufrufen darf.
func kuechenWeg(r *http.Request) bool {
	p, m := r.URL.Path, r.Method
	switch {
	case m == http.MethodGet && (p == "/api/plan" || p == "/api/kalender" || p == "/api/rezepte" || p == "/api/regeln" || p == "/api/lebensmittel"):
		return true
	case m == http.MethodGet && strings.HasPrefix(p, "/api/lebensmittel/") && !strings.ContainsRune(p[len("/api/lebensmittel/"):], '/'):
		return true
	case m == http.MethodPost && (p == "/api/chat" || p == "/api/rezepte/erfinden" || p == "/api/plan/erzeugen"):
		return true
	case strings.HasPrefix(p, "/api/plan/tag/") && (m == http.MethodPut || m == http.MethodPost):
		return true
	case m == http.MethodGet && strings.HasPrefix(p, "/api/plan/") && strings.HasSuffix(p, "/einkauf"):
		return true
	case m == http.MethodPatch && strings.HasPrefix(p, "/api/einkauf/"):
		return true
	}
	return false
}

// kuechenSeite: die Seite selbst traegt keine Daten und darf ohne Anmeldung
// laden; eine Umleitung zur Anmeldung wuerde im Rahmen nur eine Fehlermeldung
// des Browsers zeigen.
func kuechenSeite(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/kueche.html"
}

func (d *Dienst) kuecheOeffnen(w http.ResponseWriter, r *http.Request) {
	// Der Schluessel wandert in den Anker: der geht an keinen Server und in
	// kein Protokoll; die Seite merkt ihn sich und raeumt die Adresse auf.
	ziel := "/kueche.html"
	if d.kuechenSchluesselPasst(r) {
		ziel += "#k=" + d.kuechenSchluessel()
	}
	http.Redirect(w, r, ziel, http.StatusFound)
}

// ---------- Neues Rezept auf Zuruf ----------

var erfindAnweisung = `Du schreibst ein Rezept für eine Rezeptsammlung, so wie es in einem guten Hauskochbuch steht.
Antworte ausschließlich mit JSON in genau dieser Form:
{"titel":"","beschreibung":"","portionen":2,"dauer_min":45,
 "art":"hauptgericht","kost":"fleisch","aufwand":"mittel","gruppe":"","kueche":"",
 "saison":[],"zutaten":[{"menge":200,"einheit":"g","name":"Zwiebel","kategorie":"Obst und Gemüse"}],
 "schritte":["..."],"hinweise":""}
Regeln:
- Nennt der Wunsch ein bestimmtes Gericht, schreibst du genau dieses Gericht, so wie es
  üblicherweise gekocht wird, mit seiner echten Dauer und seinem echten Aufwand.
  Der Titel ist dann der Name des Gerichts, ohne Zusätze wie "schnell", "einfach" oder "klassisch".
- Ist kein bestimmtes Gericht genannt, entwickelst du ein passendes alltagstaugliches Abendessen.
- gruppe ist genau eine von: ` + gruppenListe() + `
- kueche ist genau eine von: ` + kuechenListe() + `
- art ist hauptgericht, beilage, fruehstueck oder nachtisch. kost ist vegan, vegetarisch, fisch oder fleisch, passend zu den Zutaten.
- aufwand ist schnell (bis 25 min), mittel (bis 60 min) oder aufwendig; dauer_min ist die ehrliche Gesamtzeit.
- einheit ist g, kg, ml, l, EL, TL, Stueck, Prise, Bund, Zehe, Dose, Packung oder leer.
- kategorie ist eine von: Obst und Gemüse, Fleisch und Fisch, Molkerei und Eier, Kühlregal, Trocken und Vorrat, Gewürze, Tiefkühl, Brot und Backwaren, Getränke, Sonstiges.
- Zutaten einzeln, keine Mengen im Namen. 4 bis 9 Schritte.
- Schreibstil wie ein Kochbuch: sachlich, knapp, ohne Werbesprache. Keine Wörter wie perfekt, köstlich,
  herrlich, himmlisch, Genuss, Geschmacksexplosion, aromatisch, saftig, goldbraun, ein Hauch von.
- beschreibung: ein kurzer sachlicher Satz, was es ist (z. B. "Schweinebauch mit Kruste, dazu Kartoffelknödel und Dunkelbiersoße.").
  Nicht mit "Ein" oder "Eine" beginnen.
- Schritte im Infinitiv, wie im Kochbuch ("Zwiebeln würfeln und in Butter glasig dünsten.").
- hinweise: ein praktischer Tipp aus der Küche oder leer.
- Das Gericht darf keines der genannten vorhandenen Rezepte sein.
- Echte deutsche Umlaute und ß verwenden (ä, ö, ü, ß), niemals ae, oe, ue oder ss als Ersatz. Keine Emojis.`

func (d *Dienst) rezeptErfinden(w http.ResponseWriter, r *http.Request) {
	var rumpf struct {
		Wunsch string `json:"wunsch"`
		TagID  int    `json:"tag_id"`
		Woche  string `json:"woche"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&rumpf); err != nil {
		fehler(w, 400, "unlesbar")
		return
	}
	ctx, abbruch := context.WithTimeout(r.Context(), 5*time.Minute)
	defer abbruch()
	rez, plan, err := d.erfinde(ctx, rumpf.Wunsch, rumpf.TagID, rumpf.Woche)
	if err != nil {
		fehler(w, 502, err.Error())
		return
	}
	antwort := map[string]any{"rezept": rez}
	if plan != nil {
		antwort["plan"] = plan
	}
	gib(w, antwort)
}

// erfinde laesst das Modell ein Rezept schreiben, legt es in den Pool und mit
// tagID gleich in den Plan der Woche.
func (d *Dienst) erfinde(ctx context.Context, wunsch string, tagID int, woche string) (Rezept, *Plan, error) {
	var auftrag strings.Builder
	wunsch = strings.TrimSpace(wunsch)
	if wunsch == "" {
		wunsch = "etwas Abwechslungsreiches, das zur Jahreszeit passt"
	}
	if len(wunsch) > 500 {
		wunsch = wunsch[:500]
	}
	if woche == "" {
		woche = MontagDerWoche(time.Now())
	}
	fmt.Fprintf(&auftrag, "Wunsch: %s\n", wunsch)
	if tagID != 0 {
		if p, err := d.ablage.PlanDerWoche(woche); err == nil && p != nil {
			for _, t := range p.Tage {
				if t.ID == tagID {
					tt, _ := time.Parse("2006-01-02", t.Tag)
					fmt.Fprintf(&auftrag, "Gekocht wird am %s", wochentagName(tt))
					if werktag(t.Tag) {
						auftrag.WriteString(" (Werktag, also eher schnell)")
					}
					auftrag.WriteString(".\n")
					if t.Rezept != nil {
						fmt.Fprintf(&auftrag, "Bisher geplant und nicht gewollt: %s\n", t.Rezept.Titel)
					}
				}
			}
		}
	}
	if regeln, err := d.ablage.Regeln(); err == nil && regeln["personen"] != "" {
		fmt.Fprintf(&auftrag, "Personen: %s\n", regeln["personen"])
	}
	vorhanden := map[string]bool{}
	if rez, err := d.ablage.Rezepte(); err == nil && len(rez) > 0 {
		var t []string
		for _, x := range rez {
			t = append(t, x.Titel)
			vorhanden[strings.ToLower(strings.TrimSpace(x.Titel))] = true
		}
		// Bei vielen Rezepten nur die Titel, die dem Wunsch nahe sind, sonst
		// fuellt die Liste den Kontext.
		if len(t) > 120 {
			var nah []string
			for _, x := range t {
				for _, w := range strings.Fields(strings.ToLower(wunsch)) {
					if len(w) > 3 && strings.Contains(strings.ToLower(x), w) {
						nah = append(nah, x)
						break
					}
				}
			}
			t = nah
		}
		if len(t) > 0 {
			fmt.Fprintf(&auftrag, "Vorhandene Rezepte (nicht wiederholen): %s\n", strings.Join(t, "; "))
		}
	}
	if v, err := d.ablage.Vorrat(); err == nil && len(v) > 0 {
		fmt.Fprintf(&auftrag, "Im Vorrat: %s\n", strings.Join(v, ", "))
	}

	// Zwei Versuche: ab und zu liefert das Modell kein brauchbares JSON.
	var neu Rezept
	var letzter error
	for versuch := 1; versuch <= 2; versuch++ {
		neu = Rezept{}
		letzter = d.planer.ki.FrageJSON(ctx, erfindAnweisung, auftrag.String(), &neu)
		if letzter == nil && (strings.TrimSpace(neu.Titel) == "" || len(neu.Zutaten) == 0) {
			letzter = fmt.Errorf("das Modell hat kein brauchbares Rezept geliefert")
		}
		if letzter == nil {
			break
		}
		log.Printf("Rezept erfinden, Versuch %d: %v", versuch, letzter)
		if ctx.Err() != nil {
			break
		}
	}
	if letzter != nil {
		return Rezept{}, nil, letzter
	}
	neu.ID = 0
	neu.Quelle = ""
	neu.Etiketten = Liste{}
	ordne(&neu)
	if vorhanden[strings.ToLower(strings.TrimSpace(neu.Titel))] {
		return Rezept{}, nil, fmt.Errorf("„%s“ gibt es schon im Pool", neu.Titel)
	}
	gespeichert, err := d.ablage.SpeichereRezept(neu)
	if err != nil {
		return Rezept{}, nil, err
	}
	if tagID == 0 {
		return gespeichert, nil, nil
	}
	if err := d.ablage.SetzeTag(tagID, gespeichert.ID, gespeichert.Hinweise); err != nil {
		return gespeichert, nil, err
	}
	p, err := d.ablage.PlanDerWoche(woche)
	if err == nil && p != nil {
		_ = d.planer.RechneEinkauf(p)
		p, _ = d.ablage.PlanDerWoche(woche)
	}
	return gespeichert, p, nil
}
