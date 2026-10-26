package main

import (
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
)

// Die Tests mit Datenbank brauchen ein leeres PostgreSQL in FOODR_TEST_DB,
// etwa postgres://foodr:foodr@localhost:5432/foodr_test?sslmode=disable.
var (
	testKatalog     *Katalog
	testKatalogFehl error
	testKatalogEins sync.Once
)

func katalogFuerTest(t *testing.T) *Katalog {
	t.Helper()
	url := os.Getenv("FOODR_TEST_DB")
	if url == "" {
		t.Skip("FOODR_TEST_DB nicht gesetzt")
	}
	testKatalogEins.Do(func() {
		var a *Ablage
		a, testKatalogFehl = OeffneAblage(url, schema)
		if testKatalogFehl == nil {
			testKatalog, testKatalogFehl = LadeKatalog(a.db)
		}
	})
	if testKatalogFehl != nil {
		t.Fatal(testKatalogFehl)
	}
	return testKatalog
}

func TestKatalogLaedtBLS(t *testing.T) {
	k := katalogFuerTest(t)
	var n int
	if err := k.db.QueryRow(`SELECT count(*) FROM lebensmittel WHERE quelle = 'bls'`).Scan(&n); err != nil || n != 7140 {
		t.Fatalf("%d BLS-Lebensmittel, erwartet 7140 (%v)", n, err)
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

func TestSuche(t *testing.T) {
	k := katalogFuerTest(t)
	t1, _, err := k.Suche(Frage{Text: "haferflocken", Grenze: 3})
	if err != nil || len(t1) == 0 || t1[0].ID != "C133000" {
		t.Fatalf("Haferflocken: %v %v", t1, err)
	}
	t2, _, err := k.Suche(Frage{Text: "hahnchen brust", Grenze: 5})
	if err != nil || len(t2) == 0 || t2[0].Gruppe != "V" {
		t.Fatalf("Hähnchenbrust: %v %v", t2, err)
	}
	t3, gesamt, err := k.Suche(Frage{Text: "kase", Gruppe: "M", Grenze: 10})
	if err != nil || gesamt < 20 || len(t3) != 10 {
		t.Fatalf("Käse: %d Treffer, %v", gesamt, err)
	}
	t4, _, err := k.Suche(Frage{Text: "100%_", Grenze: 5})
	if err != nil || len(t4) != 0 {
		t.Fatalf("Platzhalter werden nicht maskiert: %v %v", t4, err)
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
	id, err := k.SpeichereEigenes(&Lebensmittel{Name: "Omas Pesto", Werte: map[string]float64{"ENERCC": 480, "FAT": 50, "UNSINN": 3}})
	if err != nil || !strings.HasPrefix(id, "eigen-") {
		t.Fatalf("speichern: %q %v", id, err)
	}
	lm := k.Hole(id)
	if lm == nil || lm.Quelle != "eigen" {
		t.Fatal("eigenes Lebensmittel fehlt")
	}
	if v, ok := k.Wert(lm, "FAT"); !ok || v != 50 {
		t.Fatalf("Fett %v", v)
	}
	if _, ok := lm.Werte["UNSINN"]; ok {
		t.Fatal("unbekannter Nährstoff gespeichert")
	}
	if t1, _, _ := k.Suche(Frage{Text: "pesto", Grenze: 1}); len(t1) == 0 || t1[0].ID != id {
		t.Fatal("eigenes Pesto nicht zuerst")
	}
	if _, err := k.SpeichereEigenes(&Lebensmittel{ID: "E111100", Name: "Ei"}); err == nil {
		t.Fatal("BLS-Eintrag ließ sich überschreiben")
	}
	if err := k.LoescheEigenes(id); err != nil || k.Hole(id) != nil {
		t.Fatalf("löschen: %v", err)
	}
}

func TestOFFImport(t *testing.T) {
	k := katalogFuerTest(t)
	kopf := "code\tproduct_name\tbrands\tcountries_tags\tpnns_groups_2\tnutriscore_grade\tenergy-kcal_100g\tproteins_100g\tfat_100g\tcarbohydrates_100g\tsalt_100g\tvitamin-c_100g\n"
	zeilen := []string{
		"%s\tVollmilch 3,5 %%\tHofgut\ten:germany\tMilk and yogurt\ta\t64\t3.4\t3.5\t4.8\t3.2e-49\t0.001",
		"%s\t\tOhne Namen\ten:germany\t\t\t100\t1\t1\t1\t0\t",
		"%s\tKaputt\tX\ten:france\t\t\t2000\t1\t1\t1\t0\t",
	}
	var b strings.Builder
	b.WriteString(kopf)
	for i := 0; i < 1000; i++ {
		for j, z := range zeilen {
			b.WriteString(fmt.Sprintf(z, fmt.Sprintf("40%05d%d", i, j)) + "\n")
		}
	}
	if err := k.leseOFF(strings.NewReader(b.String())); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = k.db.QueryRow(`SELECT count(*) FROM produkte`).Scan(&n)
	if n != 1000 {
		t.Fatalf("%d Produkte, erwartet 1000", n)
	}
	t1, _, err := k.Suche(Frage{Text: "vollmilch hofgut", Quelle: "off", Grenze: 1})
	if err != nil || len(t1) != 1 || t1[0].Gruppe != "M" || t1[0].Nutriscore != "A" || !t1[0].DACH {
		t.Fatalf("Produkt: %+v %v", t1, err)
	}
	if c, ok := t1[0].Werte["VITC"]; !ok || c != 1 {
		t.Fatalf("Vitamin C in mg: %v", c)
	}
	if k.Hole(t1[0].ID) == nil {
		t.Fatal("Produkt nicht abrufbar")
	}
}
