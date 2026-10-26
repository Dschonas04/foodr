package main

import (
	"bufio"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Die Lebensmitteldatenbank: der Bundeslebensmittelschluessel (BLS 4.0) des
// Max Rubner-Instituts, 7.140 Lebensmittel mit 138 Naehrstoffen je 100 g,
// dazu die eigenen Lebensmittel aus der Ablage. Alles liegt im Speicher;
// 7.000 Zeilen durchsucht man schneller, als eine Datenbank antwortet.

//go:embed daten/naehrstoffe.tsv daten/bls.tsv.gz
var blsDateien embed.FS

const blsQuelle = "BLS 4.0"

// Fehlt ein Wert, ist er nicht bestimmt; Spuren liegen unter der Nachweisgrenze.
var (
	fehlt = float32(math.NaN())
	spur  = float32(-1)
)

type Naehrstoff struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	NameEN  string `json:"name_en"`
	Einheit string `json:"einheit"`
	Gruppe  string `json:"gruppe"`
}

type Lebensmittel struct {
	ID        string
	Name      string
	NameEN    string
	Kategorie string // Hauptgruppe, z. B. "G" fuer Gemuese, "eigen" fuer eigene
	Quelle    string
	StueckG   float64 // Gewicht eines Stuecks, 0 = unbekannt
	werte     []float32
	such      string // gefalteter Name fuer die Suche
	zusammen  string // derselbe ohne Leerzeichen: "Hafer Flocken" findet "haferflocken"
}

// Die Hauptgruppen des BLS nach dem ersten Buchstaben des Schluessels.
var Kategorien = []struct{ Code, Name string }{
	{"B", "Brot und Kleingebäck"},
	{"C", "Getreide und Mehl"},
	{"D", "Kuchen, Gebäck und Dauerbackwaren"},
	{"E", "Eier und Teigwaren"},
	{"F", "Obst"},
	{"G", "Gemüse"},
	{"H", "Hülsenfrüchte, Nüsse und Sojaprodukte"},
	{"K", "Kartoffeln und Stärke"},
	{"M", "Milch und Käse"},
	{"N", "Alkoholfreie Getränke"},
	{"P", "Alkoholische Getränke"},
	{"Q", "Fette und Öle"},
	{"R", "Gewürze, Salz und Würzmittel"},
	{"S", "Zucker, Honig und Süßwaren"},
	{"T", "Fisch und Meeresfrüchte"},
	{"U", "Fleisch"},
	{"V", "Geflügel, Wild und Innereien"},
	{"W", "Wurst und Fleischwaren"},
	{"X", "Beilagen, Gemüsegerichte und Soßen"},
	{"Y", "Gerichte und Desserts"},
	{"eigen", "Eigene Lebensmittel"},
}

func kategorieName(code string) string {
	for _, k := range Kategorien {
		if k.Code == code {
			return k.Name
		}
	}
	return code
}

// Die Werte, die an Rezepten und Plaenen stehen. Alles andere zeigt die
// Detailansicht eines Lebensmittels.
var Kernwerte = []string{"ENERCC", "PROT625", "FAT", "FASAT", "CHO", "SUGAR", "FIBT", "NACL"}

type Katalog struct {
	mu     sync.RWMutex
	stoffe []Naehrstoff
	stelle map[string]int
	liste  []*Lebensmittel
	nachID map[string]*Lebensmittel
	eigene []*Lebensmittel
}

func LadeKatalog() (*Katalog, error) {
	k := &Katalog{stelle: map[string]int{}, nachID: map[string]*Lebensmittel{}}

	roh, err := blsDateien.ReadFile("daten/naehrstoffe.tsv")
	if err != nil {
		return nil, err
	}
	for _, zeile := range strings.Split(strings.TrimSpace(string(roh)), "\n") {
		f := strings.Split(zeile, "\t")
		if len(f) < 5 {
			return nil, fmt.Errorf("naehrstoffe.tsv: kaputte Zeile %q", zeile)
		}
		k.stelle[f[0]] = len(k.stoffe)
		k.stoffe = append(k.stoffe, Naehrstoff{Code: f[0], Name: f[1], NameEN: f[2], Einheit: f[3], Gruppe: f[4]})
	}

	datei, err := blsDateien.Open("daten/bls.tsv.gz")
	if err != nil {
		return nil, err
	}
	defer datei.Close()
	gz, err := gzip.NewReader(datei)
	if err != nil {
		return nil, err
	}
	if err := k.leseBLS(gz); err != nil {
		return nil, err
	}
	return k, nil
}

func (k *Katalog) leseBLS(r io.Reader) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for s.Scan() {
		f := strings.Split(s.Text(), "\t")
		if len(f) != 3+len(k.stoffe) {
			return fmt.Errorf("bls.tsv: %d statt %d Spalten bei %q", len(f), 3+len(k.stoffe), f[0])
		}
		lm := &Lebensmittel{ID: f[0], Name: f[1], NameEN: f[2], Kategorie: f[0][:1], Quelle: blsQuelle,
			werte: make([]float32, len(k.stoffe))}
		for i, v := range f[3:] {
			switch v {
			case "":
				lm.werte[i] = fehlt
			case "~":
				lm.werte[i] = spur
			default:
				z, err := strconv.ParseFloat(v, 32)
				if err != nil {
					return fmt.Errorf("bls.tsv: %s/%s: %w", f[0], k.stoffe[i].Code, err)
				}
				lm.werte[i] = float32(z)
			}
		}
		lm.setzeSuche()
		k.liste = append(k.liste, lm)
		k.nachID[lm.ID] = lm
	}
	return s.Err()
}

// Die eigenen Lebensmittel kommen aus der Ablage und werden bei jeder
// Aenderung komplett ersetzt.
func (k *Katalog) SetzeEigene(eigene []EigenesLebensmittel) {
	neu := make([]*Lebensmittel, 0, len(eigene))
	for _, e := range eigene {
		lm := &Lebensmittel{ID: eigeneID(e.ID), Name: e.Name, Kategorie: "eigen", Quelle: "eigen",
			StueckG: e.StueckG, werte: make([]float32, len(k.stoffe))}
		for i := range lm.werte {
			lm.werte[i] = fehlt
		}
		for code, wert := range e.Werte {
			if i, ok := k.stelle[code]; ok {
				lm.werte[i] = float32(wert)
			}
		}
		lm.setzeSuche()
		neu = append(neu, lm)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, alt := range k.eigene {
		delete(k.nachID, alt.ID)
	}
	for _, lm := range neu {
		k.nachID[lm.ID] = lm
	}
	k.eigene = neu
}

func (lm *Lebensmittel) setzeSuche() {
	lm.such = falte(lm.Name)
	lm.zusammen = strings.ReplaceAll(lm.such, " ", "")
}

func eigeneID(n int) string { return "eigen-" + strconv.Itoa(n) }

func (k *Katalog) Hole(id string) *Lebensmittel {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.nachID[id]
}

func (k *Katalog) Anzahl() int {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return len(k.liste) + len(k.eigene)
}

// Wert je 100 g; ok=false, wenn er nicht bestimmt ist. Spuren zaehlen als 0.
func (k *Katalog) Wert(lm *Lebensmittel, code string) (float64, bool) {
	i, ok := k.stelle[code]
	if !ok {
		return 0, false
	}
	v := lm.werte[i]
	if v != v { // NaN
		return 0, false
	}
	if v < 0 {
		return 0, true
	}
	return glatt(v), true
}

// glatt gibt einen float32 so zurueck, wie er in der Quelle stand: 37.9
// statt 37.900001525878906.
func glatt(v float32) float64 {
	z, _ := strconv.ParseFloat(strconv.FormatFloat(float64(v), 'g', -1, 32), 64)
	return z
}

// ---------- Suche ----------

var akzente = strings.NewReplacer("ä", "a", "ö", "o", "ü", "u", "ß", "ss",
	"à", "a", "á", "a", "â", "a", "ç", "c", "è", "e", "é", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "í", "i", "ñ", "n", "ô", "o", "ó", "o", "û", "u", "ù", "u", "ú", "u")

// falte macht aus "Crème fraîche" und "Hähnchen" etwas, das sich ohne
// Akzente und Umlaute vergleichen laesst.
func falte(s string) string {
	s = strings.ToLower(s)
	s = akzente.Replace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

type Treffer struct {
	lm     *Lebensmittel
	punkte float64
}

// Suche findet Lebensmittel, deren Name jedes Suchwort enthaelt. Was mit dem
// ersten Wort beginnt, roh ist und kurz heisst, steht vorne.
func (k *Katalog) Suche(frage, kategorie string, grenze int) []*Lebensmittel {
	woerter := strings.Fields(falte(frage))
	k.mu.RLock()
	defer k.mu.RUnlock()
	var treffer []Treffer
	pruefe := func(lm *Lebensmittel) {
		if kategorie != "" && lm.Kategorie != kategorie {
			return
		}
		p, ok := bewerteTreffer(lm, woerter)
		if ok {
			treffer = append(treffer, Treffer{lm, p})
		}
	}
	for _, lm := range k.eigene {
		pruefe(lm)
	}
	for _, lm := range k.liste {
		pruefe(lm)
	}
	sort.SliceStable(treffer, func(i, j int) bool { return treffer[i].punkte > treffer[j].punkte })
	if grenze > 0 && len(treffer) > grenze {
		treffer = treffer[:grenze]
	}
	raus := make([]*Lebensmittel, len(treffer))
	for i, t := range treffer {
		raus[i] = t.lm
	}
	return raus
}

func bewerteTreffer(lm *Lebensmittel, woerter []string) (float64, bool) {
	if len(woerter) == 0 {
		return -float64(len(lm.such)) / 10, true
	}
	p := 0.0
	namensWoerter := strings.Fields(lm.such)
	for _, w := range woerter {
		if !strings.Contains(lm.such, w) {
			// Der BLS schreibt "Hafer Flocken" und "Weizen Mehl".
			if !strings.Contains(lm.zusammen, w) {
				return 0, false
			}
			p += 18
			continue
		}
		for i, nw := range namensWoerter {
			if nw == w {
				p += 20
				if i == 0 {
					p += 10
				}
			} else if strings.HasPrefix(nw, w) {
				p += 8
			}
		}
	}
	if strings.HasPrefix(lm.zusammen, woerter[0]) {
		p += 25
	}
	if strings.Contains(" "+lm.such+" ", " roh ") {
		p += 12
	}
	if lm.Kategorie == "eigen" {
		p += 15
	}
	// Fertige Gerichte, Kuchen und tiefgefrorene Ware nur, wenn sonst nichts passt.
	switch lm.Kategorie {
	case "X", "Y", "D":
		p -= 20
	}
	if strings.Contains(lm.such, "tiefgefroren") {
		p -= 6
	}
	p -= float64(len(lm.such)) / 4
	return p, true
}
