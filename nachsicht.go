package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Kleine Modelle halten sich nicht immer an die Form: aus einer Liste wird ein
// String, aus einer Zahl "etwas". Statt die Antwort zu verwerfen, liest die
// App sie nachsichtig -- die Regeln stehen weiter im Prompt, aber ein
// verrutschtes Feld kostet nicht den ganzen Plan.

type Liste []string

func (l *Liste) UnmarshalJSON(daten []byte) error {
	var alsListe []string
	if err := json.Unmarshal(daten, &alsListe); err == nil {
		*l = alsListe
		return nil
	}
	var alsText string
	if err := json.Unmarshal(daten, &alsText); err == nil {
		*l = teileText(alsText)
		return nil
	}
	// Auch Zahlen oder null sollen nicht stoeren.
	*l = Liste{}
	return nil
}

func teileText(s string) Liste {
	raus := Liste{}
	for _, teil := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == '/' || r == '|'
	}) {
		if t := strings.TrimSpace(teil); t != "" {
			raus = append(raus, t)
		}
	}
	return raus
}

type Zahl float64

func (z *Zahl) UnmarshalJSON(daten []byte) error {
	var alsZahl float64
	if err := json.Unmarshal(daten, &alsZahl); err == nil {
		*z = Zahl(alsZahl)
		return nil
	}
	var alsText string
	if err := json.Unmarshal(daten, &alsText); err == nil {
		alsText = strings.TrimSpace(strings.ReplaceAll(alsText, ",", "."))
		// "1/2 Bund" und aehnliche Brueche kommen vor.
		if vor, nach, ist := strings.Cut(alsText, "/"); ist {
			a, f1 := strconv.ParseFloat(strings.TrimSpace(vor), 64)
			b, f2 := strconv.ParseFloat(strings.TrimSpace(nach), 64)
			if f1 == nil && f2 == nil && b != 0 {
				*z = Zahl(a / b)
				return nil
			}
		}
		if f, err := strconv.ParseFloat(alsText, 64); err == nil {
			*z = Zahl(f)
			return nil
		}
	}
	*z = 0 // "etwas", "nach Geschmack", null
	return nil
}

// Ein vegetarisches Gericht als "fleisch" einzustufen faellt spaetestens beim
// Wochenplan auf. Was in den Zutaten steht, gilt mehr als das Etikett.
func kostAusZutaten(zutaten []Zutat, vorgabe string) string {
	fleisch, fisch, tierisch := false, false, false
	for _, z := range zutaten {
		switch kategorieRaten(z.Name) {
		case "Fleisch und Fisch":
			if istFisch(z.Name) {
				fisch = true
			} else {
				fleisch = true
			}
		case "Molkerei und Eier":
			tierisch = true
		}
		if enthaeltHonig(z.Name) {
			tierisch = true
		}
	}
	switch {
	case fleisch:
		return "fleisch"
	case fisch:
		return "fisch"
	case tierisch:
		return "vegetarisch"
	case len(zutaten) == 0:
		return vorgabe
	default:
		return "vegan"
	}
}

func istFisch(name string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"lachs", "thunfisch", "garnele", "kabeljau", "forelle", "fisch",
		"scampi", "muschel", "sardine", "hering", "makrele"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

func enthaeltHonig(name string) bool {
	return strings.Contains(strings.ToLower(name), "honig")
}
