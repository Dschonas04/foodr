package main

import (
	"fmt"
	"sort"
	"strings"
)

// Zutaten eines Rezepts auf Lebensmittel abbilden und daraus Naehrwerte
// rechnen. Die Zuordnung laeuft von selbst; was sie falsch trifft, legt man
// einmal von Hand fest, das gilt dann fuer jedes Rezept mit dieser Zutat.

// Was die Suche allein nicht findet oder schlecht trifft: Zutat (gefaltet)
// auf BLS-Schluessel.
var bekannteZutaten = map[string]string{
	"ei": "E111100", "eier": "E111100", "eigelb": "E112100", "eiweiss": "E113100",
	"karotte": "G620100", "karotten": "G620100", "mohre": "G620100", "mohren": "G620100",
	"hackfleisch": "U050100", "hackfleisch gemischt": "U050100", "gemischtes hackfleisch": "U050100",
	"rinderhack": "U010100", "rinderhackfleisch": "U010100",
	"hahnchenbrust": "V416100", "hahnchenbrustfilet": "V416100", "hahnchen": "V413000",
	"reis": "C352000", "basmatireis": "C352000", "jasminreis": "C352000", "langkornreis": "C352000",
	"risottoreis": "C352000", "vollkornreis": "C351000",
	"spinat": "G211100", "blattspinat": "G211100", "brokkoli": "G312100",
	"creme fraiche": "M176800", "schmand": "M172700", "saure sahne": "M172500", "sauerrahm": "M172500",
	"sahne": "M173800", "schlagsahne": "M173800", "kochsahne": "M173800",
	"feta": "M012200", "feta im stuck": "M012200", "schafskase": "M012200",
	"fladenbrot":  "B782100",
	"gemusebruhe": "X416243", "bruhe": "X416243", "rinderbruhe": "U985200", "huhnerbruhe": "X411243",
	"kartoffel": "K110100", "kartoffeln": "K110100", "susskartoffel": "K420100",
	"kidneybohnen": "H742902", "kidneybohne": "H742902",
	"tomate": "G561100", "tomaten": "G561100", "kirschtomate": "G561100", "kirschtomaten": "G561100",
	"cocktailtomaten": "G561100", "passierte tomaten": "R161200", "stuckige tomaten": "G568900",
	"dosentomaten": "G568900", "geschalte tomaten": "G568900", "tomatenmark": "R160000",
	"knoblauch": "G490100", "zwiebel": "G480100", "zwiebeln": "G480100", "rote zwiebel": "G480100",
	"rote zwiebeln": "G480100", "fruhlingszwiebel": "G482100", "fruhlingszwiebeln": "G482100",
	"kokosmilch": "H154000", "kurbis": "G581000", "hokkaidokurbis": "G581000", "hokkaido": "G581000",
	"lachs": "T410100", "lachsfilet": "T410100", "limette": "F602100", "zitrone": "F601100",
	"mais": "G570902", "olivenol": "Q120000", "ol": "Q180000", "rapsol": "Q180000",
	"sonnenblumenol": "Q320000", "kurbiskernol": "Q250000", "sesamol": "Q230000",
	"paprika": "G543100", "rote paprika": "G543100", "gelbe paprika": "G542100", "grune paprika": "G541100",
	"parmesan": "M306400", "geriebener kase": "M304600", "reibekase": "M304600", "kase": "M400600",
	"gouda": "M402600", "mozzarella": "M032100", "emmentaler": "M304600",
	"nudeln": "E401000", "pasta": "E401000", "penne": "E401000", "spaghetti": "E401000",
	"fusilli": "E401000", "tagliatelle": "E432000", "makkaroni": "E401000", "rigatoni": "E401000",
	"farfalle": "E401000", "bandnudeln": "E432000", "spatzle": "X711512", "gnocchi": "K270000",
	"pfeffer": "R258100", "salz": "R111000", "rotwein": "P2A3000",
	"rindergulasch": "U151100", "gulasch": "U151100", "sesam": "H420100",
	"sojasauce": "R143000", "sojasosse": "R143000", "sojaschnetzel": "H765000",
	"speck": "W411300", "speckwurfel": "W411300", "bacon": "W415000",
	"stange sellerie": "G220100", "staudensellerie": "G220100", "bleichsellerie": "G220100",
	"sellerie": "G660100", "zartbitterschokolade": "S560000", "zucchini": "G582100",
	"rote linsen": "H730000", "linsen": "H725100", "ingwer": "R211200", "basilikum": "G061000",
	"butter": "Q611000", "milch": "M111300", "mehl": "C214100", "weizenmehl": "C214100", "zucker": "S111000",
	"haferflocken": "C133000", "joghurt": "M141300", "naturjoghurt": "M141300",
	"apfel": "F110100", "apfel roh": "F110100", "banane": "F503100", "bananen": "F503100",
	"chili": "G554100", "chilischote": "G554100", "peperoni": "G554100",
	"quark": "M713100", "magerquark": "M713100", "speisequark": "M713100",
}

// Ein Stueck wiegt je nach Zutat sehr verschieden.
var stueckGewichte = []struct {
	Wort  string
	Gramm float64
}{
	{"kirschtomate", 15}, {"cocktailtomate", 15}, {"knoblauch", 50}, {"fruhlingszwiebel", 20},
	{"zwiebel", 100}, {"kartoffel", 150}, {"karotte", 80}, {"mohre", 80}, {"paprika", 160},
	{"zucchini", 250}, {"tomate", 100}, {"zitrone", 100}, {"limette", 60}, {"apfel", 150},
	{"banane", 120}, {"gurke", 400}, {"aubergine", 300}, {"kurbis", 1200}, {"hokkaido", 1200},
	{"fladenbrot", 400}, {"ingwer", 20}, {"sellerie", 50}, {"lauch", 200}, {"chili", 15},
	{"brokkoli", 500}, {"blumenkohl", 800}, {"avocado", 200}, {"ei", 60}, {"eier", 60}, {"eigelb", 20}, {"lachsfilet", 125},
	{"hahnchenbrust", 150}, {"brotchen", 60}, {"schokolade", 100}, {"feta", 200}, {"mozzarella", 125},
}

// Grammzahl einer Einheit, wo sie nicht vom Lebensmittel abhaengt.
var einheitGramm = map[string]float64{
	"g": 1, "kg": 1000, "ml": 1, "l": 1000, "EL": 15, "TL": 5, "Prise": 0.4,
	"Bund": 30, "Zehe": 4, "Dose": 400, "Packung": 250, "Scheibe": 30, "Becher": 200, "Glas": 350,
}

// Gramm fuer eine Zutat; ok=false, wenn die Menge sich nicht umrechnen laesst.
func zutatGramm(z Zutat, lm *Lebensmittel, stueckVonHand float64) (float64, bool) {
	menge := float64(z.Menge)
	if menge <= 0 {
		return 0, false
	}
	einheit := normEinheit(z.Einheit)
	if einheit == "Stück" || einheit == "" {
		g := stueckVonHand
		if g <= 0 && lm != nil {
			g = lm.StueckG
		}
		if g <= 0 {
			g = stueckGewicht(z.Name)
		}
		if g <= 0 {
			return 0, false
		}
		return menge * g, true
	}
	if g, ok := einheitGramm[einheit]; ok {
		return menge * g, true
	}
	return 0, false
}

func stueckGewicht(name string) float64 {
	f := falte(name)
	for _, s := range stueckGewichte {
		for _, w := range strings.Fields(f) {
			// Kurze Woerter wie "ei" nur ganz, sonst wiegt ein Eisbergsalat 60 g.
			if w == s.Wort || len(s.Wort) >= 4 && (strings.HasPrefix(w, s.Wort) || strings.HasSuffix(w, s.Wort)) {
				return s.Gramm
			}
		}
	}
	return 0
}

// Ordne sucht das Lebensmittel zu einer Zutat: erst die bekannte Liste, dann
// die Suche mit dem ganzen Namen, dann nur mit dem letzten Wort (im Deutschen
// steht das Hauptwort hinten: "geriebener Käse"), dann ohne Mehrzahlendung.
func (k *Katalog) Ordne(zutat string) *Lebensmittel {
	f := falte(zutat)
	if f == "" {
		return nil
	}
	if code, ok := bekannteZutaten[f]; ok {
		if lm := k.Hole(code); lm != nil {
			return lm
		}
	}
	versuche := []string{f}
	if w := strings.Fields(f); len(w) > 1 {
		versuche = append(versuche, w[len(w)-1])
	}
	for _, v := range append([]string(nil), versuche...) {
		if code, ok := bekannteZutaten[v]; ok && v != f {
			if lm := k.Hole(code); lm != nil {
				return lm
			}
		}
		for _, endung := range []string{"en", "n", "e", "s"} {
			if strings.HasSuffix(v, endung) && len(v)-len(endung) >= 4 {
				versuche = append(versuche, strings.TrimSuffix(v, endung))
			}
		}
	}
	for _, v := range versuche {
		for _, lm := range k.kandidaten(v) {
			if passtZurZutat(lm, v) {
				return lm
			}
		}
	}
	return nil
}

// kandidaten sucht nur unter Grundlebensmitteln (BLS) und eigenen; ein
// Markenprodukt waehlt man von Hand.
func (k *Katalog) kandidaten(text string) []*Lebensmittel {
	woerter := strings.Fields(text)
	if len(woerter) == 0 {
		return nil
	}
	bed := []string{"quelle IN ('bls','eigen')"}
	args := []any{}
	for _, w := range woerter {
		args = append(args, "%"+likeMaske.Replace(w)+"%")
		bed = append(bed, fmt.Sprintf("such LIKE $%d", len(args)))
	}
	zeilen, err := k.db.Query(`SELECT `+alleSpalten+` FROM lebensmittel WHERE `+strings.Join(bed, " AND ")+
		` LIMIT 500`, args...)
	if err != nil {
		return nil
	}
	defer zeilen.Close()
	type treffer struct {
		lm     *Lebensmittel
		punkte float64
	}
	var liste []treffer
	for zeilen.Next() {
		lm, err := leseLebensmittel(zeilen)
		if err != nil {
			return nil
		}
		if p, ok := bewerteTreffer(lm, woerter); ok {
			liste = append(liste, treffer{lm, p})
		}
	}
	sort.SliceStable(liste, func(i, j int) bool {
		if liste[i].punkte != liste[j].punkte {
			return liste[i].punkte > liste[j].punkte
		}
		return liste[i].lm.ID < liste[j].lm.ID
	})
	raus := make([]*Lebensmittel, 0, 20)
	for i := 0; i < len(liste) && i < 20; i++ {
		raus = append(raus, liste[i].lm)
	}
	return raus
}

// Die Suche ist grosszuegig; fuer eine Zuordnung ohne Rueckfrage muss das
// Lebensmittel mit der Zutat anfangen oder sie als ganzes Wort enthalten,
// und es darf kein fertiges Gericht sein ("Dill" ist keine Dillcreme mit
// Krabben, "Chili" kein Chili sin carne).
func passtZurZutat(lm *Lebensmittel, zutat string) bool {
	switch lm.Gruppe {
	case "X", "Y":
		return false
	}
	such := falte(lm.Name)
	if strings.HasPrefix(strings.ReplaceAll(such, " ", ""), strings.ReplaceAll(zutat, " ", "")) {
		return true
	}
	for _, w := range strings.Fields(such) {
		if w == zutat {
			return true
		}
	}
	return false
}

// ---------- Rechnen ----------

// Eine Zuordnung auf Auslassen nimmt die Zutat aus der Rechnung, etwa Wasser.
const Auslassen = "-"

type Zuordnung struct {
	Zutat        string  `json:"zutat"`
	Lebensmittel string  `json:"lebensmittel"`
	StueckG      float64 `json:"stueck_g"`
}

type ZutatWerte struct {
	Zutat
	Lebensmittel *LebensmittelKurz  `json:"lebensmittel"`
	VonHand      bool               `json:"von_hand"`
	Gramm        float64            `json:"gramm"`
	Werte        map[string]float64 `json:"werte,omitempty"`
	Fehlt        string             `json:"fehlt,omitempty"`
}

type RezeptWerte struct {
	RezeptID  int                `json:"rezept_id"`
	Portionen int                `json:"portionen"`
	Gesamt    map[string]float64 `json:"gesamt"`
	JePortion map[string]float64 `json:"je_portion"`
	Zutaten   []ZutatWerte       `json:"zutaten"`
	Erfasst   int                `json:"erfasst"` // Zutaten mit Werten
}

// Rechne bildet die Kernwerte eines Rezepts. Zutaten ohne Zuordnung oder
// ohne umrechenbare Menge zaehlen nicht mit und stehen mit Grund in der Liste.
func (k *Katalog) Rechne(r Rezept, vonHand map[string]Zuordnung) RezeptWerte {
	portionen := r.Portionen
	if portionen <= 0 {
		portionen = 1
	}
	raus := RezeptWerte{RezeptID: r.ID, Portionen: portionen, Gesamt: leereWerte(), JePortion: leereWerte()}
	for _, z := range r.Zutaten {
		zw := ZutatWerte{Zutat: z}
		var lm *Lebensmittel
		stueck := 0.0
		if h, ok := vonHand[falte(z.Name)]; ok {
			if h.Lebensmittel == Auslassen {
				zw.VonHand = true
				zw.Fehlt = "bewusst ausgelassen"
				raus.Zutaten = append(raus.Zutaten, zw)
				continue
			}
			lm = k.Hole(h.Lebensmittel)
			stueck = h.StueckG
			zw.VonHand = true
		}
		if lm == nil && !zw.VonHand {
			lm = k.Ordne(z.Name)
		}
		if lm == nil {
			zw.Fehlt = "kein Lebensmittel zugeordnet"
			raus.Zutaten = append(raus.Zutaten, zw)
			continue
		}
		zw.Lebensmittel = k.Kurz(lm)
		g, ok := zutatGramm(z, lm, stueck)
		if !ok {
			zw.Fehlt = "Menge nicht in Gramm umrechenbar"
			raus.Zutaten = append(raus.Zutaten, zw)
			continue
		}
		zw.Gramm = runde(g)
		zw.Werte = map[string]float64{}
		for _, code := range Kernwerte {
			if v, ok := k.Wert(lm, code); ok {
				anteil := v * g / 100
				zw.Werte[code] = runde(anteil)
				raus.Gesamt[code] += anteil
			}
		}
		raus.Erfasst++
		raus.Zutaten = append(raus.Zutaten, zw)
	}
	for code, v := range raus.Gesamt {
		raus.Gesamt[code] = runde(v)
		raus.JePortion[code] = runde(v / float64(portionen))
	}
	return raus
}

func leereWerte() map[string]float64 {
	m := make(map[string]float64, len(Kernwerte))
	for _, c := range Kernwerte {
		m[c] = 0
	}
	return m
}

// ---------- Ausgabe ----------

// LebensmittelKurz ist eine Zeile in Suchlisten und Zuordnungen.
type LebensmittelKurz struct {
	ID         string             `json:"id"`
	Quelle     string             `json:"quelle"`
	Name       string             `json:"name"`
	Marke      string             `json:"marke,omitempty"`
	Gruppe     string             `json:"gruppe,omitempty"`
	Menge      string             `json:"menge,omitempty"`
	Nutriscore string             `json:"nutriscore,omitempty"`
	Bild       string             `json:"bild,omitempty"`
	StueckG    float64            `json:"stueck_g,omitempty"`
	Kern       map[string]float64 `json:"kern"`
}

func (k *Katalog) Kurz(lm *Lebensmittel) *LebensmittelKurz {
	kurz := &LebensmittelKurz{ID: lm.ID, Quelle: lm.Quelle, Name: lm.Name, Marke: lm.Marke, Gruppe: lm.Gruppe,
		Menge: lm.Menge, Nutriscore: lm.Nutriscore, Bild: lm.Bild, StueckG: lm.StueckG, Kern: map[string]float64{}}
	for _, c := range Kernwerte {
		if v, ok := k.Wert(lm, c); ok {
			kurz.Kern[c] = rundeSig(v)
		}
	}
	return kurz
}

type StoffWert struct {
	Naehrstoff
	Wert float64 `json:"wert"`
	Spur bool    `json:"spur,omitempty"`
}

type StoffGruppe struct {
	Name   string      `json:"name"`
	Stoffe []StoffWert `json:"stoffe"`
}

// LebensmittelVoll ist die Detailansicht: alles, was bekannt ist.
type LebensmittelVoll struct {
	*Lebensmittel
	GruppeName string             `json:"gruppe_name"`
	QuelleName string             `json:"quelle_name"`
	Lizenz     string             `json:"lizenz,omitempty"`
	Adresse    string             `json:"adresse,omitempty"`
	Kern       map[string]float64 `json:"kern"`
	Gruppen    []StoffGruppe      `json:"gruppen"`
	Werte      map[string]float64 `json:"werte"`
}

func (k *Katalog) Voll(lm *Lebensmittel) LebensmittelVoll {
	v := LebensmittelVoll{Lebensmittel: lm, GruppeName: kategorieName(lm.Gruppe), Kern: k.Kurz(lm).Kern,
		Werte: map[string]float64{}}
	for _, q := range Quellen {
		if q.Code == lm.Quelle {
			v.QuelleName, v.Lizenz, v.Adresse = q.Name, q.Lizenz, q.Adresse
		}
	}
	if lm.Quelle == "off" && lm.Barcode != "" {
		v.Adresse = "https://world.openfoodfacts.org/product/" + lm.Barcode
	}
	stelle := map[string]int{}
	for _, s := range k.stoffe {
		w, ok := lm.Werte[s.Code]
		if !ok {
			continue
		}
		sw := StoffWert{Naehrstoff: s, Wert: w}
		if w < 0 {
			sw.Wert, sw.Spur = 0, true
		}
		v.Werte[s.Code] = sw.Wert
		g, ok := stelle[s.Gruppe]
		if !ok {
			g = len(v.Gruppen)
			stelle[s.Gruppe] = g
			v.Gruppen = append(v.Gruppen, StoffGruppe{Name: s.Gruppe})
		}
		v.Gruppen[g].Stoffe = append(v.Gruppen[g].Stoffe, sw)
	}
	return v
}
