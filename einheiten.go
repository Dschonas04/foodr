package main

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Einheiten und Kategorien zusammenfassen: eine Einkaufsliste ist nur dann
// brauchbar, wenn 200 g und 0,3 kg Hackfleisch als eine Zeile erscheinen.

var einheitenKarte = map[string]string{
	"g": "g", "gr": "g", "gramm": "g",
	"kg": "kg", "kilo": "kg", "kilogramm": "kg",
	"ml": "ml", "milliliter": "ml",
	"l": "l", "liter": "l",
	"el": "EL", "esslöffel": "EL", "esslöffeln": "EL",
	"tl": "TL", "teelöffel": "TL", "teelöffeln": "TL",
	"stk": "Stück", "st": "Stück", "stück": "Stück", "stueck": "Stück",
	"prise": "Prise", "prisen": "Prise",
	"bund": "Bund", "zehe": "Zehe", "zehen": "Zehe",
	"dose": "Dose", "dosen": "Dose", "packung": "Packung", "pck": "Packung",
	"scheibe": "Scheibe", "scheiben": "Scheibe", "glas": "Glas", "becher": "Becher",
	"": "",
}

func normEinheit(e string) string {
	k := strings.ToLower(strings.TrimSpace(e))
	if v, ok := einheitenKarte[k]; ok {
		return v
	}
	// "Knoblauchzehen" ist eine Zehe, "Teelöffel voll" ein TL.
	for teil, einheit := range map[string]string{"zehe": "Zehe", "prise": "Prise", "bund": "Bund",
		"dose": "Dose", "packung": "Packung", "scheib": "Scheibe", "stück": "Stück",
		"stueck": "Stück", "glas": "Glas", "becher": "Becher", "esslöffel": "EL",
		"teelöffel": "TL", "gramm": "g", "liter": "l"} {
		if strings.Contains(k, teil) {
			return einheit
		}
	}
	return strings.TrimSpace(e)
}

// Was das Modell in die Einheit geschrieben hat, ist manchmal Teil des Namens
// ("0 Pfeffer Pfeffer"). Diese Zeile wird hier geradegezogen.
func raeumeZutat(z *Zutat) {
	z.Name = strings.TrimSpace(z.Name)
	roh := strings.TrimSpace(z.Einheit)
	z.Einheit = normEinheit(roh)
	if z.Einheit != "" {
		if _, bekannt := einheitenKarte[strings.ToLower(z.Einheit)]; !bekannt {
			// Unbekannte Einheit: sie gehoert in den Namen, nicht in die Spalte.
			if !strings.Contains(strings.ToLower(z.Name), strings.ToLower(z.Einheit)) {
				z.Name = strings.TrimSpace(z.Einheit + " " + z.Name)
			}
			z.Einheit = ""
		}
	}
	z.Name = ohneDoppelwort(z.Name)
	z.Kategorie = kategorieRaten(z.Name)
}

func ohneDoppelwort(name string) string {
	woerter := strings.Fields(name)
	raus := make([]string, 0, len(woerter))
	for i, w := range woerter {
		if i > 0 && strings.EqualFold(w, woerter[i-1]) {
			continue
		}
		raus = append(raus, w)
	}
	return strings.Join(raus, " ")
}

// Auf eine Grundeinheit bringen, damit sich Mengen addieren lassen.
func grund(menge float64, einheit string) (float64, string) {
	switch einheit {
	case "kg":
		return menge * 1000, "g"
	case "l":
		return menge * 1000, "ml"
	default:
		return menge, einheit
	}
}

// Und wieder zurueck in eine Groesse, die man am Regal liest.
func lesbar(menge float64, einheit string) (float64, string) {
	if einheit == "g" && menge >= 1000 {
		return runde(menge / 1000), "kg"
	}
	if einheit == "ml" && menge >= 1000 {
		return runde(menge / 1000), "l"
	}
	return runde(menge), einheit
}

func runde(f float64) float64 {
	return math.Round(f*100) / 100
}

// 333,33 g Haehnchenbrust kauft niemand. Mengen werden auf das gerundet, was
// an der Theke oder im Regal tatsaechlich geht.
func einkaufsmenge(menge float64, einheit string) float64 {
	if menge <= 0 {
		return 0
	}
	switch einheit {
	case "g", "ml":
		if menge < 30 {
			return math.Ceil(menge)
		}
		if menge < 120 {
			return math.Ceil(menge/5) * 5
		}
		return math.Ceil(menge/10) * 10
	case "EL", "TL", "Prise":
		return math.Ceil(menge*2) / 2
	default:
		// Stueck, Zehe, Bund, Dose, Packung: halbe Zwiebeln gibt es nicht.
		return math.Ceil(menge)
	}
}

var kategorieWorte = map[string][]string{
	"Obst und Gemüse": {"apfel", "banane", "zitrone", "limette", "orange", "beere", "tomate",
		"gurke", "zwiebel", "knoblauch", "kartoffel", "karotte", "möhre", "paprika", "salat",
		"spinat", "brokkoli", "blumenkohl", "zucchini", "aubergine", "pilz", "champignon",
		"lauch", "porree", "sellerie", "kürbis", "kohl", "erbse", "bohne", "ingwer",
		"petersilie", "basilikum", "schnittlauch", "koriander", "dill", "rucola", "avocado",
		"mango", "birne", "traube", "kräuter", "sprossen", "radieschen", "rote bete", "fenchel",
		"kirschtomate", "blattspinat"},
	"Fleisch und Fisch": {"hackfleisch", "rind", "rindergulasch", "schwein", "hähnchen", "huhn",
		"pute", "geflügel", "speck", "schinken", "wurst", "bratwurst", "salami", "lachs",
		"thunfisch", "garnele", "kabeljau", "forelle", "fisch", "steak", "gulasch", "lamm",
		"ente", "chorizo", "lachsfilet", "hähnchenbrust"},
	"Molkerei und Eier": {"milch", "sahne", "butter", "joghurt", "quark", "käse", "feta",
		"mozzarella", "parmesan", "schmand", "creme fraiche", "crème fraîche", "ei", "eier",
		"frischkäse", "halloumi", "skyr", "buttermilch", "hefe", "geriebener käse"},
	"Trocken und Vorrat": {"mehl", "zucker", "salz", "pfeffer", "reis", "basmatireis", "nudel",
		"pasta", "spaghetti", "penne", "linse", "kichererbse", "couscous", "bulgur", "quinoa",
		"haferflocken", "öl", "olivenöl", "kürbiskernöl", "essig", "senf", "honig", "tomatenmark",
		"passierte tomaten", "stückige tomaten", "brühe", "gemüsebrühe", "rinderbrühe", "gewürz",
		"paprikapulver", "chili", "chiliflocken", "curry", "currypulver", "kreuzkümmel", "oregano", "thymian", "zimt",
		"muskat", "chiliflocken", "backpulver", "nuss", "nüsse", "mandel", "walnuss", "sesam", "sojasauce",
		"kokosmilch", "polenta", "brot", "brötchen", "fladenbrot", "toast", "wrap", "tortilla",
		"schokolade", "zartbitterschokolade", "vanille", "stärke", "kakao", "rosine", "datteln",
		"tofu", "seitan", "sojaschnetzel", "kidneybohne", "mais", "dose", "spätzle", "rotwein"},
	"Getränke": {"wein", "bier", "saft", "wasser", "cola", "kaffee", "tee", "prosecco", "sekt"},
	"Kühlregal": {"pesto", "tortelloni", "ravioli", "blätterteig", "pizzateig", "flammkuchenteig",
		"gnocchi", "räuchertofu"},
	"Tiefkühl": {"tiefkühl", "gefrorene"},
}

// Teilstrings taeuschen: "Reis" steckt in keinem "Ei", aber "ei" steckt in
// "Reis". Deshalb zaehlt nur, was als Wort oder als Wortanfang auftritt, und
// von mehreren Treffern gewinnt der laengste -- "Kuerbiskernoel" ist Vorrat,
// nicht Gemuese.
func kategorieRaten(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	woerter := strings.FieldsFunc(n, func(r rune) bool {
		return !unicode.IsLetter(r) && r != 'ß'
	})
	beste, laenge := "Sonstiges", 0
	for kategorie, worte := range kategorieWorte {
		for _, w := range worte {
			if len(w) <= laenge {
				continue
			}
			if !trifft(n, woerter, w) {
				continue
			}
			beste, laenge = kategorie, len(w)
		}
	}
	return beste
}

func trifft(ganz string, woerter []string, wort string) bool {
	if strings.Contains(wort, " ") {
		return strings.Contains(ganz, wort)
	}
	for _, w := range woerter {
		if w == wort {
			return true
		}
		// Zusammengesetzte Woerter: "Haehnchenbrust" faengt mit "Haehnchen" an.
		if len(wort) >= 4 && (strings.HasPrefix(w, wort) || strings.HasSuffix(w, wort)) {
			return true
		}
	}
	return false
}

// Schluessel, unter dem zwei Zutaten als dieselbe gelten.
func zutatSchluessel(name, einheit string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimFunc(n, func(r rune) bool { return !unicode.IsLetter(r) })
	for _, weg := range []string{"frische ", "frischer ", "frisches ", "gehackte ", "gehackter ",
		"getrocknete ", "getrockneter ", "gemahlener ", "gemahlene "} {
		n = strings.TrimPrefix(n, weg)
	}
	return n + "|" + einheit
}

// Aus den Rezepten eines Plans eine Einkaufsliste rechnen: auf Personen
// skaliert, zusammengefasst, ohne alles, was im Vorrat steht.
func Einkaufsliste(tage []PlanTag, personen int, vorrat []string) []Posten {
	imVorrat := func(name string) bool {
		n := strings.ToLower(name)
		for _, v := range vorrat {
			v = strings.ToLower(strings.TrimSpace(v))
			if v != "" && strings.Contains(n, v) {
				return true
			}
		}
		return false
	}
	summe := map[string]*Posten{}
	for _, t := range tage {
		if t.Rezept == nil {
			continue
		}
		faktor := 1.0
		if t.Rezept.Portionen > 0 && personen > 0 {
			faktor = float64(personen) / float64(t.Rezept.Portionen)
		}
		for _, z := range t.Rezept.Zutaten {
			if strings.TrimSpace(z.Name) == "" || imVorrat(z.Name) {
				continue
			}
			menge, einheit := grund(float64(z.Menge)*faktor, normEinheit(z.Einheit))
			s := zutatSchluessel(z.Name, einheit)
			if p, da := summe[s]; da {
				p.Menge += menge
				continue
			}
			kat := z.Kategorie
			if kat == "" {
				kat = kategorieRaten(z.Name)
			}
			summe[s] = &Posten{Name: saubererName(z.Name), Menge: menge, Einheit: einheit, Kategorie: kat}
		}
	}
	liste := make([]Posten, 0, len(summe))
	for _, p := range summe {
		p.Menge = einkaufsmenge(p.Menge, p.Einheit)
		p.Menge, p.Einheit = lesbar(p.Menge, p.Einheit)
		liste = append(liste, *p)
	}
	sort.Slice(liste, func(i, j int) bool {
		if liste[i].Kategorie != liste[j].Kategorie {
			return liste[i].Kategorie < liste[j].Kategorie
		}
		return strings.ToLower(liste[i].Name) < strings.ToLower(liste[j].Name)
	})
	return liste
}

func saubererName(n string) string {
	n = strings.TrimSpace(n)
	if n == "" {
		return n
	}
	r := []rune(n)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
