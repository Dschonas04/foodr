package main

// Die Datenstrukturen, die zwischen Ablage, KI und Oberflaeche wandern.
// Feldnamen bleiben deutsch, damit die JSON-Schnittstelle lesbar bleibt.

type Zutat struct {
	Menge     Zahl   `json:"menge"`
	Einheit   string `json:"einheit"`
	Name      string `json:"name"`
	Kategorie string `json:"kategorie"`
}

type Rezept struct {
	ID             int      `json:"id"`
	Titel          string   `json:"titel"`
	Beschreibung   string   `json:"beschreibung"`
	Portionen      int      `json:"portionen"`
	DauerMin       int      `json:"dauer_min"`
	Art            string   `json:"art"`     // hauptgericht, beilage, fruehstueck, nachtisch
	Kost           string   `json:"kost"`    // vegan, vegetarisch, fisch, fleisch
	Aufwand        string   `json:"aufwand"` // schnell, mittel, aufwendig
	Saison         Liste    `json:"saison"`
	Etiketten      Liste    `json:"etiketten"`
	Zutaten        []Zutat  `json:"zutaten"`
	Schritte       []string `json:"schritte"`
	Hinweise       string   `json:"hinweise"`
	Quelle         string   `json:"quelle"`
	Bewertung      int      `json:"bewertung"`
	ZuletztGekocht string   `json:"zuletzt_gekocht"` // YYYY-MM-DD oder leer
}

type PlanTag struct {
	ID       int     `json:"id"`
	Tag      string  `json:"tag"`
	RezeptID int     `json:"rezept_id"`
	Rezept   *Rezept `json:"rezept,omitempty"`
	Hinweis  string  `json:"hinweis"`
	Gekocht  bool    `json:"gekocht"`
}

type Plan struct {
	ID          int       `json:"id"`
	Woche       string    `json:"woche"`
	Personen    int       `json:"personen"`
	Modell      string    `json:"modell"`
	Begruendung string    `json:"begruendung"`
	Erzeugt     string    `json:"erzeugt"`
	Tage        []PlanTag `json:"tage"`
}

type Posten struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Menge     float64 `json:"menge"`
	Einheit   string  `json:"einheit"`
	Kategorie string  `json:"kategorie"`
	Abgehakt  bool    `json:"abgehakt"`
	Zusatz    bool    `json:"zusatz"`
}

// Was die Oberflaeche beim Erzeugen eines Plans mitgibt.
type Auftrag struct {
	Woche      string   `json:"woche"` // Montag, YYYY-MM-DD
	Personen   int      `json:"personen"`
	Tage       []string `json:"tage"`       // Wochentage, die bekocht werden
	Wuensche   string   `json:"wuensche"`   // Freitext an den Assistenten
	NurKost    []string `json:"nur_kost"`   // leer = alles erlaubt
	Ausschluss []int    `json:"ausschluss"` // Rezepte, die diesmal nicht sollen
}
