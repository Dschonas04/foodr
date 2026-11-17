package main

// Die Kuechenansicht (web/kueche.html): Foodr kompakt, etwa eingebettet in
// Home Assistant. Dazu zwei Dinge, die es vorher nicht gab: ein Gespraech mit
// dem Modell ueber Woche und Rezepte, und ein neues Rezept auf Zuruf, wenn im
// Pool nichts Passendes mehr liegt.
//
// Einbetten heisst: keine Anmeldeseite im Rahmen (Keycloak laesst sich nicht
// einbetten). Die Ansicht wird deshalb einmal mit einem Schluessel geoeffnet,
// /kueche?k=..., der aus dem Kennwort abgeleitet ist; Foodr setzt daraufhin
// denselben Keks wie nach der Anmeldung mit Kennwort.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	return s != "" && r.URL.Path == "/kueche" && gleich(r.URL.Query().Get("k"), s)
}

// kekserlaubt: der Keks, den die Anmeldung mit Kennwort (oder der Schluessel
// der Kuechenansicht) setzt, laesst auch bei SSO durch.
func (d *Dienst) keksErlaubt(r *http.Request) bool {
	if d.kennwort == "" {
		return false
	}
	k, err := r.Cookie("foodr")
	return err == nil && gleich(k.Value, d.merkmal())
}

func (d *Dienst) kuecheOeffnen(w http.ResponseWriter, r *http.Request) {
	if d.kuechenSchluesselPasst(r) {
		http.SetCookie(w, &http.Cookie{Name: "foodr", Value: d.merkmal(), Path: "/",
			MaxAge: 60 * 60 * 24 * 365, HttpOnly: true, SameSite: http.SameSiteStrictMode,
			Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
	}
	// Der Schluessel soll nicht in der Adresszeile oder im Verlauf stehen bleiben.
	http.Redirect(w, r, "/kueche.html", http.StatusFound)
}

// ---------- Gespraech ----------

const chatAnweisung = `Du bist der Kochassistent von Foodr, einem Essensplaner fuer einen Haushalt.
Antworte auf Deutsch, knapp und praktisch, mit echten Umlauten, ohne Emojis.
Du kennst den Wochenplan, die Rezepte im Pool, den Vorrat und die Planungsregeln (unten).
Wenn jemand fragt, was es gibt, was er einkaufen soll, was er aus Resten kochen kann,
wie ein Gericht geht oder womit er etwas ersetzen kann, antworte aus diesen Daten.
Wenn ein Gericht nicht passt, schlage ein anderes aus dem Pool vor und nenne den Titel genau.
Erfinde keine Dinge ueber den Plan, die nicht unten stehen.`

func (d *Dienst) chatKontext() string {
	var b strings.Builder
	jetzt := time.Now()
	fmt.Fprintf(&b, "Heute ist %s, %s.\n\n", wochentagName(jetzt), jetzt.Format("02.01.2006"))
	if p, err := d.ablage.PlanDerWoche(MontagDerWoche(jetzt)); err == nil && p != nil {
		b.WriteString("Wochenplan:\n")
		for _, t := range p.Tage {
			titel := "nichts geplant"
			if t.Rezept != nil {
				titel = t.Rezept.Titel
			}
			gekocht := ""
			if t.Gekocht {
				gekocht = " (gekocht)"
			}
			tt, _ := time.Parse("2006-01-02", t.Tag)
			fmt.Fprintf(&b, "- %s %s: %s%s\n", wochentagName(tt), tt.Format("02.01."), titel, gekocht)
			if t.Rezept != nil {
				var z []string
				for _, zu := range t.Rezept.Zutaten {
					z = append(z, zu.Name)
				}
				fmt.Fprintf(&b, "  Zutaten: %s\n", strings.Join(z, ", "))
				if len(t.Rezept.Schritte) > 0 {
					fmt.Fprintf(&b, "  Zubereitung: %s\n", strings.Join(t.Rezept.Schritte, " "))
				}
			}
		}
		b.WriteString("\n")
	}
	if rez, err := d.ablage.Rezepte(); err == nil {
		b.WriteString("Rezepte im Pool:\n")
		for _, r := range rez {
			fmt.Fprintf(&b, "- %s (%s, %d min, %s)\n", r.Titel, r.Kost, r.DauerMin, r.Aufwand)
		}
		b.WriteString("\n")
	}
	if v, err := d.ablage.Vorrat(); err == nil && len(v) > 0 {
		fmt.Fprintf(&b, "Immer im Vorrat: %s\n", strings.Join(v, ", "))
	}
	if r, err := d.ablage.Regeln(); err == nil {
		if r["wuensche"] != "" {
			fmt.Fprintf(&b, "Wuensche fuer die Planung: %s\n", r["wuensche"])
		}
		if r["personen"] != "" {
			fmt.Fprintf(&b, "Personen: %s\n", r["personen"])
		}
	}
	return b.String()
}

func wochentagName(t time.Time) string {
	return [...]string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}[t.Weekday()]
}

func (d *Dienst) chat(w http.ResponseWriter, r *http.Request) {
	var rumpf struct {
		Verlauf []nachricht `json:"verlauf"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&rumpf); err != nil || len(rumpf.Verlauf) == 0 {
		fehler(w, 400, "unlesbar")
		return
	}
	// Nur die letzten Wortwechsel, und nur Nutzer und Assistent: eine Rolle
	// "system" aus dem Browser wuerde die Anweisung ueberschreiben.
	verlauf := rumpf.Verlauf
	if len(verlauf) > 16 {
		verlauf = verlauf[len(verlauf)-16:]
	}
	nachrichten := []nachricht{{Rolle: "system", Inhalt: chatAnweisung + "\n\n" + d.chatKontext()}}
	for _, n := range verlauf {
		if n.Rolle != "user" && n.Rolle != "assistant" {
			continue
		}
		if len(n.Inhalt) > 4000 {
			n.Inhalt = n.Inhalt[:4000]
		}
		nachrichten = append(nachrichten, n)
	}
	ctx, abbruch := context.WithTimeout(r.Context(), 4*time.Minute)
	defer abbruch()
	antwort, err := d.planer.ki.FrageText(ctx, nachrichten)
	if err != nil {
		fehler(w, 502, err.Error())
		return
	}
	gib(w, map[string]any{"antwort": antwort})
}

// ---------- Neues Rezept auf Zuruf ----------

const erfindAnweisung = `Du entwickelst ein neues Rezept fuer einen Haushalt.
Antworte ausschliesslich mit JSON in genau dieser Form:
{"titel":"","beschreibung":"","portionen":2,"dauer_min":30,
 "art":"hauptgericht","kost":"vegetarisch","aufwand":"schnell",
 "saison":[],"etiketten":[],
 "zutaten":[{"menge":200,"einheit":"g","name":"Nudeln","kategorie":"Trocken und Vorrat"}],
 "schritte":["..."],"hinweise":"","quelle":""}
Regeln:
- Ein alltagstaugliches Abendessen, das zum Wunsch passt.
- art ist hauptgericht. kost ist vegan, vegetarisch, fisch oder fleisch, passend zu den Zutaten.
- aufwand ist schnell (bis 25 min), mittel (bis 60 min) oder aufwendig.
- einheit ist g, kg, ml, l, EL, TL, Stueck, Prise, Bund, Zehe, Dose, Packung oder leer.
- kategorie ist eine von: Obst und Gemüse, Fleisch und Fisch, Molkerei und Eier, Kühlregal, Trocken und Vorrat, Gewürze, Tiefkühl, Brot und Backwaren, Getränke, Sonstiges.
- Zutaten einzeln, keine Mengen im Namen. 4 bis 8 kurze Schritte.
- Das Gericht darf keines der genannten vorhandenen Rezepte sein.
- Deutsche Umlaute ausschreiben, keine Emojis.`

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
	var auftrag strings.Builder
	wunsch := strings.TrimSpace(rumpf.Wunsch)
	if wunsch == "" {
		wunsch = "etwas Abwechslungsreiches, das zur Jahreszeit passt"
	}
	if len(wunsch) > 500 {
		wunsch = wunsch[:500]
	}
	fmt.Fprintf(&auftrag, "Wunsch: %s\n", wunsch)
	if rumpf.TagID != 0 {
		woche := rumpf.Woche
		if woche == "" {
			woche = MontagDerWoche(time.Now())
		}
		if p, err := d.ablage.PlanDerWoche(woche); err == nil && p != nil {
			for _, t := range p.Tage {
				if t.ID == rumpf.TagID {
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
	if rez, err := d.ablage.Rezepte(); err == nil && len(rez) > 0 {
		var t []string
		for _, x := range rez {
			t = append(t, x.Titel)
		}
		fmt.Fprintf(&auftrag, "Vorhandene Rezepte: %s\n", strings.Join(t, "; "))
	}
	if v, err := d.ablage.Vorrat(); err == nil && len(v) > 0 {
		fmt.Fprintf(&auftrag, "Im Vorrat: %s\n", strings.Join(v, ", "))
	}

	ctx, abbruch := context.WithTimeout(r.Context(), 5*time.Minute)
	defer abbruch()
	var neu Rezept
	if err := d.planer.ki.FrageJSON(ctx, erfindAnweisung, auftrag.String(), &neu); err != nil {
		fehler(w, 502, err.Error())
		return
	}
	neu.ID = 0
	neu.Quelle = "Foodr-Assistent"
	ordne(&neu)
	if strings.TrimSpace(neu.Titel) == "" || len(neu.Zutaten) == 0 {
		fehler(w, 422, "das Modell hat kein brauchbares Rezept geliefert")
		return
	}
	gespeichert, err := d.ablage.SpeichereRezept(neu)
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	antwort := map[string]any{"rezept": gespeichert}
	if rumpf.TagID != 0 {
		if err := d.ablage.SetzeTag(rumpf.TagID, gespeichert.ID, gespeichert.Hinweise); err != nil {
			fehler(w, 500, err.Error())
			return
		}
		woche := rumpf.Woche
		if woche == "" {
			woche = MontagDerWoche(time.Now())
		}
		if p, err := d.ablage.PlanDerWoche(woche); err == nil && p != nil {
			_ = d.planer.RechneEinkauf(p)
			p, _ = d.ablage.PlanDerWoche(woche)
			antwort["plan"] = p
		}
	}
	gib(w, antwort)
}
