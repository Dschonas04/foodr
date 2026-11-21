package main

// Kalender: die geplanten Gerichte ueber Wochen hinweg, als JSON fuer die
// Monatsansicht der Kueche und als iCalendar fuer Home Assistant.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type KalenderTag struct {
	PlanTag
	Woche string `json:"woche"`
}

func (a *Ablage) Kalender(von, bis string) ([]KalenderTag, error) {
	zeilen, err := a.db.Query(`SELECT t.id, t.tag, COALESCE(t.rezept_id, 0), t.hinweis, t.gekocht, p.woche
		FROM plan_tage t JOIN plaene p ON p.id = t.plan_id
		WHERE t.tag BETWEEN $1 AND $2 ORDER BY t.tag`, von, bis)
	if err != nil {
		return nil, err
	}
	defer zeilen.Close()
	tage := []KalenderTag{}
	for zeilen.Next() {
		var t KalenderTag
		var tag, woche time.Time
		if err := zeilen.Scan(&t.ID, &tag, &t.RezeptID, &t.Hinweis, &t.Gekocht, &woche); err != nil {
			return nil, err
		}
		t.Tag, t.Woche = tag.Format("2006-01-02"), woche.Format("2006-01-02")
		tage = append(tage, t)
	}
	if err := zeilen.Err(); err != nil {
		return nil, err
	}
	for i := range tage {
		if tage[i].RezeptID != 0 {
			if r, err := a.Rezept(tage[i].RezeptID); err == nil {
				tage[i].Rezept = &r
			}
		}
	}
	return tage, nil
}

// zeitraum liest von/bis (YYYY-MM-DD), hoechstens ein gutes Jahr.
func zeitraum(r *http.Request, vorher, nachher int) (string, string, bool) {
	jetzt := time.Now()
	von, bis := jetzt.AddDate(0, 0, -vorher), jetzt.AddDate(0, 0, nachher)
	if s := r.URL.Query().Get("von"); s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return "", "", false
		}
		von = t
	}
	if s := r.URL.Query().Get("bis"); s != "" {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return "", "", false
		}
		bis = t
	}
	if bis.Before(von) || bis.Sub(von) > 400*24*time.Hour {
		return "", "", false
	}
	return von.Format("2006-01-02"), bis.Format("2006-01-02"), true
}

func (d *Dienst) kalender(w http.ResponseWriter, r *http.Request) {
	von, bis, ok := zeitraum(r, 31, 31)
	if !ok {
		fehler(w, 400, "Zeitraum ungültig")
		return
	}
	tage, err := d.ablage.Kalender(von, bis)
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	gib(w, tage)
}

// kalenderSchluessel oeffnet nur den iCalendar-Feed (Home Assistant kann
// keine Kopfzeilen schicken), abgeleitet aus dem Kennwort.
func (d *Dienst) kalenderSchluessel() string {
	if d.kennwort == "" {
		return ""
	}
	m := hmac.New(sha256.New, []byte(d.kennwort))
	m.Write([]byte("foodr-kalender"))
	return hex.EncodeToString(m.Sum(nil))
}

func (d *Dienst) kalenderSchluesselPasst(r *http.Request) bool {
	s := d.kalenderSchluessel()
	return s != "" && r.Method == http.MethodGet && r.URL.Path == "/api/kalender.ics" && gleich(r.URL.Query().Get("k"), s)
}

// icsText maskiert nach RFC 5545 und faltet lange Zeilen.
func icsText(s string) string {
	s = strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\r", `\n`, "\n", `\n`).Replace(s)
	// uebrige Steuerzeichen raus, damit kein Text eine eigene Eigenschaft anfaengt
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029 {
			return -1
		}
		return r
	}, s)
}

func icsZeile(b *strings.Builder, schluessel, wert string) {
	z := schluessel + ":" + wert
	for len(z) > 74 {
		n := 74
		for n > 0 && (z[n]&0xC0) == 0x80 { // nicht mitten in einem UTF-8-Zeichen
			n--
		}
		b.WriteString(z[:n] + "\r\n")
		z = " " + z[n:]
	}
	b.WriteString(z + "\r\n")
}

func (d *Dienst) kalenderICS(w http.ResponseWriter, r *http.Request) {
	von, bis, ok := zeitraum(r, 60, 60)
	if !ok {
		fehler(w, 400, "Zeitraum ungültig")
		return
	}
	tage, err := d.ablage.Kalender(von, bis)
	if err != nil {
		fehler(w, 500, err.Error())
		return
	}
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Foodr//Essensplan//DE\r\nCALSCALE:GREGORIAN\r\n")
	icsZeile(&b, "X-WR-CALNAME", "Foodr")
	stempel := time.Now().UTC().Format("20060102T150405Z")
	for _, t := range tage {
		if t.Rezept == nil {
			continue
		}
		tag, _ := time.Parse("2006-01-02", t.Tag)
		b.WriteString("BEGIN:VEVENT\r\n")
		icsZeile(&b, "UID", fmt.Sprintf("foodr-tag-%d@foodr", t.ID))
		icsZeile(&b, "DTSTAMP", stempel)
		icsZeile(&b, "DTSTART;VALUE=DATE", tag.Format("20060102"))
		icsZeile(&b, "DTEND;VALUE=DATE", tag.AddDate(0, 0, 1).Format("20060102"))
		titel := t.Rezept.Titel
		if t.Gekocht {
			titel = "✓ " + titel
		}
		icsZeile(&b, "SUMMARY", icsText(titel))
		teile := []string{}
		if t.Rezept.DauerMin > 0 {
			teile = append(teile, fmt.Sprintf("%d min", t.Rezept.DauerMin))
		}
		for _, x := range []string{t.Rezept.Kost, t.Rezept.Aufwand, t.Rezept.Kueche} {
			if x != "" {
				teile = append(teile, x)
			}
		}
		text := strings.Join(teile, " · ")
		if t.Hinweis != "" {
			text += "\n" + t.Hinweis
		}
		if t.Rezept.Beschreibung != "" {
			text += "\n\n" + t.Rezept.Beschreibung
		}
		icsZeile(&b, "DESCRIPTION", icsText(text))
		b.WriteString("TRANSP:TRANSPARENT\r\nEND:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(b.String()))
}
