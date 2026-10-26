package main

import (
	"encoding/json"
	"testing"
)

// Was kleine Modelle so abliefern, darf nicht den ganzen Plan kosten.
func TestNachsichtigesEinlesen(t *testing.T) {
	roh := `{"titel":"Ofen-Feta-Pasta","portionen":4,"dauer_min":35,"kost":"fleisch",
		"saison":"","etiketten":"Auflauf, schnell",
		"zutaten":[{"menge":"500","einheit":"g","name":"Kirschtomaten"},
		           {"menge":"1/2","einheit":"Bund","name":"Basilikum"},
		           {"menge":"etwas","einheit":"","name":"Salz"},
		           {"menge":200,"einheit":"g","name":"Feta"}],
		"schritte":["backen"]}`
	var r Rezept
	if err := json.Unmarshal([]byte(roh), &r); err != nil {
		t.Fatalf("sollte lesbar sein: %v", err)
	}
	if len(r.Etiketten) != 2 {
		t.Errorf("Etiketten: %v", r.Etiketten)
	}
	if len(r.Saison) != 0 {
		t.Errorf("Saison sollte leer sein: %v", r.Saison)
	}
	if r.Zutaten[1].Menge != 0.5 {
		t.Errorf("halber Bund: %v", r.Zutaten[1].Menge)
	}
	if r.Zutaten[2].Menge != 0 {
		t.Errorf("etwas Salz: %v", r.Zutaten[2].Menge)
	}
	ordne(&r)
	if r.Kost != "vegetarisch" {
		t.Errorf("ohne Fleisch in den Zutaten darf es nicht fleisch sein: %s", r.Kost)
	}
}

func TestKostAusZutaten(t *testing.T) {
	faelle := []struct {
		zutaten []Zutat
		will    string
	}{
		{[]Zutat{{Name: "Lachsfilet"}, {Name: "Sahne"}}, "fisch"},
		{[]Zutat{{Name: "Hackfleisch"}, {Name: "Nudeln"}}, "fleisch"},
		{[]Zutat{{Name: "Linsen"}, {Name: "Kokosmilch"}}, "vegan"},
		{[]Zutat{{Name: "Nudeln"}, {Name: "Parmesan"}}, "vegetarisch"},
	}
	for _, f := range faelle {
		if raus := kostAusZutaten(f.zutaten, "fleisch"); raus != f.will {
			t.Errorf("%v: %s statt %s", f.zutaten, raus, f.will)
		}
	}
}
