package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Rezepte wandern selten getippt in den Pool, sondern kopiert: aus einer
// Nachricht, von einer Seite, aus einem Kochbuch abgeschrieben. Das Modell
// bringt den Wust in die Form, die die Ablage erwartet.

const einleseAnweisung = `Du liest ein Rezept und gibst es strukturiert zurueck.
Antworte ausschliesslich mit JSON:
{"titel":"","beschreibung":"","portionen":2,"dauer_min":30,
 "art":"hauptgericht","kost":"fleisch","aufwand":"mittel",
 "saison":["Sommer"],"etiketten":["Auflauf"],
 "zutaten":[{"menge":200,"einheit":"g","name":"Hackfleisch","kategorie":"Fleisch und Fisch"}],
 "schritte":["..."],"hinweise":"","quelle":""}
Regeln:
- art ist hauptgericht, beilage, fruehstueck oder nachtisch.
- kost ist vegan, vegetarisch, fisch oder fleisch — richte dich nach den Zutaten.
- aufwand ist schnell (bis 25 min), mittel (bis 60 min) oder aufwendig.
- einheit ist g, kg, ml, l, EL, TL, Stueck, Prise, Bund, Zehe, Dose, Packung oder leer.
- menge ist eine Zahl; "etwas" oder "nach Geschmack" wird 0 mit passender Einheit.
- Zutaten einzeln auffuehren, keine Mengen im Namen.
- schritte sind kurze, vollstaendige Saetze in der Reihenfolge des Kochens.
- saison nur setzen, wenn das Gericht wirklich saisonal ist, sonst leer lassen.
- Nichts erfinden, was nicht im Text steht; fehlende Angaben schaetzen und in
  hinweise vermerken.
- Deutsche Umlaute ausschreiben, keine Emojis.`

func (p *Planer) Einlesen(ctx context.Context, text, quelle string) (Rezept, error) {
	var r Rezept
	text = strings.TrimSpace(text)
	if quelle == "" && strings.HasPrefix(text, "http") && !strings.ContainsAny(text, " \n") {
		quelle = text
	}
	if quelle != "" && strings.HasPrefix(quelle, "http") && len(text) < 200 {
		geholt, err := holeSeite(ctx, quelle)
		if err != nil {
			return r, err
		}
		text = geholt
	}
	if len(text) < 20 {
		return r, fmt.Errorf("zu wenig Text, um ein Rezept daraus zu lesen")
	}
	if len(text) > 12000 {
		text = text[:12000]
	}
	if err := p.ki.FrageJSON(ctx, einleseAnweisung, "Rezept:\n\n"+text, &r); err != nil {
		return r, err
	}
	r.ID = 0
	if r.Quelle == "" {
		r.Quelle = quelle
	}
	ordne(&r)
	if strings.TrimSpace(r.Titel) == "" {
		return r, fmt.Errorf("das Modell hat keinen Titel erkannt")
	}
	return r, nil
}

// Go rechnet mit RE2, also ohne Rueckverweise: jedes Element bekommt seinen
// eigenen Ausdruck.
var (
	marken  = regexp.MustCompile(`(?s)<[^>]+>`)
	leeren  = regexp.MustCompile(`\n{3,}`)
	beiwerk = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`),
		regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`),
		regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`),
		regexp.MustCompile(`(?is)<footer[^>]*>.*?</footer>`),
		regexp.MustCompile(`(?is)<header[^>]*>.*?</header>`),
		regexp.MustCompile(`(?is)<!--.*?-->`),
	}
)

func holeSeite(ctx context.Context, url string) (string, error) {
	anfrage, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	anfrage.Header.Set("User-Agent", "Foodr/1.0 (Essensplaner)")
	klient := &http.Client{Timeout: 20 * time.Second}
	antwort, err := klient.Do(anfrage)
	if err != nil {
		return "", fmt.Errorf("Seite nicht erreichbar: %w", err)
	}
	defer antwort.Body.Close()
	if antwort.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Seite antwortet %d", antwort.StatusCode)
	}
	roh, err := io.ReadAll(io.LimitReader(antwort.Body, 2<<20))
	if err != nil {
		return "", err
	}
	text := string(roh)
	for _, a := range beiwerk {
		text = a.ReplaceAllString(text, " ")
	}
	text = marken.ReplaceAllString(text, "\n")
	for alt, neu := range map[string]string{"&nbsp;": " ", "&amp;": "&", "&quot;": "\"",
		"&#39;": "'", "&auml;": "ä", "&ouml;": "ö", "&uuml;": "ü", "&szlig;": "ß",
		"&Auml;": "Ä", "&Ouml;": "Ö", "&Uuml;": "Ü", "&lt;": "<", "&gt;": ">"} {
		text = strings.ReplaceAll(text, alt, neu)
	}
	zeilen := []string{}
	for _, z := range strings.Split(text, "\n") {
		if z = strings.TrimSpace(z); z != "" {
			zeilen = append(zeilen, z)
		}
	}
	return leeren.ReplaceAllString(strings.Join(zeilen, "\n"), "\n\n"), nil
}
