package main

import "testing"

func TestKategorieRaten(t *testing.T) {
	faelle := map[string]string{
		"Reis":                 "Trocken und Vorrat",
		"Basmatireis":          "Trocken und Vorrat",
		"Ei":                   "Molkerei und Eier",
		"Flammkuchenteig":      "Kühlregal",
		"Kürbiskernöl":         "Trocken und Vorrat",
		"Hokkaidokürbis":       "Obst und Gemüse",
		"passierte Tomaten":    "Trocken und Vorrat",
		"Kirschtomate":         "Obst und Gemüse",
		"Kidneybohnen":         "Trocken und Vorrat",
		"Hähnchenbrust":        "Fleisch und Fisch",
		"Speckwürfel":          "Fleisch und Fisch",
		"geriebener Käse":      "Molkerei und Eier",
		"Sojaschnetzel":        "Trocken und Vorrat",
		"Gnocchi":              "Kühlregal",
		"Zartbitterschokolade": "Trocken und Vorrat",
		"Wunderzeug":           "Sonstiges",
	}
	for name, erwartet := range faelle {
		if raus := kategorieRaten(name); raus != erwartet {
			t.Errorf("%s: %s statt %s", name, raus, erwartet)
		}
	}
}

func TestEinkaufsmenge(t *testing.T) {
	faelle := []struct {
		menge   float64
		einheit string
		will    float64
	}{
		{333.33, "g", 340}, {2.33, "Stück", 3}, {0.5, "Stück", 1}, {0.67, "EL", 1},
		{166.67, "g", 170}, {23.33, "g", 24}, {12.2, "g", 13}, {0.1, "TL", 0.5},
	}
	for _, f := range faelle {
		if raus := einkaufsmenge(f.menge, f.einheit); raus != f.will {
			t.Errorf("%v %s: %v statt %v", f.menge, f.einheit, raus, f.will)
		}
	}
}

// Zwei Rezepte, dieselbe Zutat in verschiedenen Einheiten: eine Zeile.
func TestEinkaufslisteFasstZusammen(t *testing.T) {
	tage := []PlanTag{
		{Rezept: &Rezept{Portionen: 2, Zutaten: []Zutat{{Menge: 200, Einheit: "g", Name: "Hackfleisch"}}}},
		{Rezept: &Rezept{Portionen: 2, Zutaten: []Zutat{{Menge: 0.3, Einheit: "kg", Name: "Hackfleisch"},
			{Menge: 1, Einheit: "Stück", Name: "Salz"}}}},
	}
	liste := Einkaufsliste(tage, 2, []string{"Salz"})
	if len(liste) != 1 {
		t.Fatalf("erwarte eine Zeile, bekomme %d: %+v", len(liste), liste)
	}
	if liste[0].Menge != 500 || liste[0].Einheit != "g" {
		t.Errorf("erwarte 500 g, bekomme %v %s", liste[0].Menge, liste[0].Einheit)
	}
}

// Auf vier Personen skaliert ein Rezept fuer zwei sich genau.
func TestEinkaufslisteSkaliert(t *testing.T) {
	tage := []PlanTag{{Rezept: &Rezept{Portionen: 2, Zutaten: []Zutat{{Menge: 250, Einheit: "g", Name: "Nudeln"}}}}}
	liste := Einkaufsliste(tage, 4, nil)
	if liste[0].Menge != 500 {
		t.Errorf("erwarte 500 g, bekomme %v", liste[0].Menge)
	}
}

func TestRaeumeZutat(t *testing.T) {
	faelle := []struct {
		ein  Zutat
		name string
		einh string
		kat  string
	}{
		{Zutat{Menge: 3, Einheit: "Knoblauchzehen", Name: "Knoblauch"}, "Knoblauch", "Zehe", "Obst und Gemüse"},
		{Zutat{Einheit: "Pfeffer", Name: "Pfeffer"}, "Pfeffer", "", "Trocken und Vorrat"},
		{Zutat{Menge: 200, Einheit: "gr", Name: "Feta im Stück"}, "Feta im Stück", "g", "Molkerei und Eier"},
		{Zutat{Menge: 1, Einheit: "Handvoll", Name: "Nüsse"}, "Handvoll Nüsse", "", "Trocken und Vorrat"},
	}
	for _, f := range faelle {
		z := f.ein
		raeumeZutat(&z)
		if z.Name != f.name || z.Einheit != f.einh || z.Kategorie != f.kat {
			t.Errorf("%+v -> %q %q %q", f.ein, z.Name, z.Einheit, z.Kategorie)
		}
	}
}

func TestNurOeffentlicheAdressen(t *testing.T) {
	for _, a := range []string{"127.0.0.1:80", "10.0.0.1:443", "192.168.1.5:80", "169.254.169.254:80",
		"[::1]:443", "[fd00::1]:80", "100.64.1.1:80", "0.0.0.0:80", "198.18.0.1:80",
		"[64:ff9b::a00:1]:80", "[2002:a00:1::1]:443", "240.0.0.1:80"} {
		if nurOeffentlich("tcp", a, nil) == nil {
			t.Errorf("%s sollte gesperrt sein", a)
		}
	}
	if err := nurOeffentlich("tcp", "93.184.216.34:443", nil); err != nil {
		t.Errorf("öffentliche Adresse gesperrt: %v", err)
	}
}

func TestEinerVon(t *testing.T) {
	if einerVon(" Vegan ", "vegan", "fleisch") != "vegan" || einerVon("<img>", "vegan") != "" {
		t.Fatal("einerVon lässt Falsches durch")
	}
}
