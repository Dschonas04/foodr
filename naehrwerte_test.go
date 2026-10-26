package main

import (
	"math"
	"testing"
)

func katalogFuerTest(t *testing.T) *Katalog {
	t.Helper()
	k, err := LadeKatalog()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestKatalogLaedtBLS(t *testing.T) {
	k := katalogFuerTest(t)
	if n := k.Anzahl(); n != 7140 {
		t.Fatalf("%d Lebensmittel, erwartet 7140", n)
	}
	ei := k.Hole("E111100")
	if ei == nil || ei.Name != "Hühnerei roh" {
		t.Fatalf("Hühnerei fehlt: %+v", ei)
	}
	if kcal, ok := k.Wert(ei, "ENERCC"); !ok || kcal != 135 {
		t.Fatalf("Hühnerei %v kcal", kcal)
	}
}

func TestOrdneTrifftGrundzutaten(t *testing.T) {
	k := katalogFuerTest(t)
	faelle := map[string]string{
		"Ei":                   "E111100",
		"Zwiebeln":             "G480100",
		"Rote Zwiebel":         "G480100",
		"Knoblauch":            "G490100",
		"Hokkaidokürbis":       "G581000",
		"geriebener Käse":      "M304600",
		"Crème fraîche":        "M176800",
		"Broccoli":             "G312100",
		"Zucchini":             "G582100",
		"Olivenöl":             "Q120000",
		"Gnocchi":              "K270000",
		"Kidneybohnen":         "H742902",
		"Hackfleisch gemischt": "U050100",
		"Speisequark":          "M713100",
		"Weizenmehl":           "C214100",
		"Chili":                "G554100",
	}
	for zutat, code := range faelle {
		lm := k.Ordne(zutat)
		if lm == nil {
			t.Errorf("%s: nichts gefunden", zutat)
			continue
		}
		if lm.ID != code {
			t.Errorf("%s: %s %q, erwartet %s", zutat, lm.ID, lm.Name, code)
		}
	}
}

func TestOrdneNimmtKeineGerichte(t *testing.T) {
	k := katalogFuerTest(t)
	for _, zutat := range []string{"Dill", "Thymian", "Oregano", "Muskat"} {
		if lm := k.Ordne(zutat); lm != nil {
			t.Errorf("%s: %s %q, erwartet keine Zuordnung", zutat, lm.ID, lm.Name)
		}
	}
}

func TestSucheZusammengeschrieben(t *testing.T) {
	k := katalogFuerTest(t)
	if t1 := k.Suche("haferflocken", "", 1); len(t1) == 0 || t1[0].ID != "C133000" {
		t.Fatalf("Haferflocken: %v", t1)
	}
}

func TestSucheOhneUmlaute(t *testing.T) {
	k := katalogFuerTest(t)
	t1 := k.Suche("hahnchen brust", "", 5)
	if len(t1) == 0 || t1[0].Kategorie != "V" {
		t.Fatalf("Hähnchenbrust nicht gefunden: %v", t1)
	}
	if len(k.Suche("kase", "M", 0)) < 20 {
		t.Fatal("zu wenig Käse")
	}
}

func TestRechneRezept(t *testing.T) {
	k := katalogFuerTest(t)
	r := Rezept{ID: 1, Portionen: 2, Zutaten: []Zutat{
		{Menge: 4, Einheit: "Stück", Name: "Eier"},
		{Menge: 2, Einheit: "EL", Name: "Olivenöl"},
		{Menge: 1, Einheit: "Prise", Name: "Salz"},
		{Menge: 0, Einheit: "", Name: "Pfeffer"},
		{Menge: 1, Einheit: "Stück", Name: "Drachenfrucht"},
	}}
	w := k.Rechne(r, map[string]Zuordnung{"drachenfrucht": {Zutat: "drachenfrucht", Lebensmittel: Auslassen}})
	// 240 g Ei zu 135 kcal + 30 g Öl zu rund 900 kcal je 100 g
	ei := 240 * 135.0 / 100
	if w.Gesamt["ENERCC"] < ei+250 || w.Gesamt["ENERCC"] > ei+290 {
		t.Fatalf("Energie %v", w.Gesamt["ENERCC"])
	}
	if math.Abs(w.JePortion["ENERCC"]*2-w.Gesamt["ENERCC"]) > 0.05 {
		t.Fatalf("je Portion %v, gesamt %v", w.JePortion["ENERCC"], w.Gesamt["ENERCC"])
	}
	if w.Erfasst != 3 {
		t.Fatalf("%d erfasst, erwartet 3", w.Erfasst)
	}
	if w.Zutaten[3].Fehlt == "" || w.Zutaten[4].Fehlt != "bewusst ausgelassen" {
		t.Fatalf("Gründe fehlen: %+v", w.Zutaten)
	}
}

func TestEigeneLebensmittel(t *testing.T) {
	k := katalogFuerTest(t)
	k.SetzeEigene([]EigenesLebensmittel{{ID: 3, Name: "Omas Pesto", StueckG: 0,
		Werte: map[string]float64{"ENERCC": 480, "FAT": 50}}})
	lm := k.Hole("eigen-3")
	if lm == nil || lm.Kategorie != "eigen" {
		t.Fatal("eigenes Lebensmittel fehlt")
	}
	if v, ok := k.Wert(lm, "FAT"); !ok || v != 50 {
		t.Fatalf("Fett %v", v)
	}
	if _, ok := k.Wert(lm, "PROT625"); ok {
		t.Fatal("Eiweiß sollte fehlen")
	}
	if t1 := k.Suche("pesto", "", 1); len(t1) == 0 || t1[0].ID != "eigen-3" {
		t.Fatal("eigenes Pesto nicht zuerst")
	}
	k.SetzeEigene(nil)
	if k.Hole("eigen-3") != nil {
		t.Fatal("gelöschtes Lebensmittel noch da")
	}
}
