package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

// neu merkt sich einen Fingerabdruck der Kennung; false, wenn sie schon da war.
func neu(gesehen map[uint64]struct{}, id string) bool {
	h := fnv.New64a()
	h.Write([]byte(id))
	f := h.Sum64()
	if _, da := gesehen[f]; da {
		return false
	}
	gesehen[f] = struct{}{}
	return true
}

// Open Food Facts: die frei gepflegte Produktdatenbank (ODbL), einige
// Millionen Markenprodukte mit Naehrwerten je 100 g. Der Export ist eine
// gezippte TSV-Datei von rund 1,5 GB; sie wird im Fluss gelesen, in eine
// neue Tabelle kopiert, indiziert und erst dann gegen die alte getauscht.
// Die Suche laeuft waehrenddessen auf dem alten Stand weiter.

const offAdresse = "https://static.openfoodfacts.org/data/en.openfoodfacts.org.products.csv.gz"

type ImportStand struct {
	mu       sync.Mutex
	Laeuft   bool   `json:"laeuft"`
	Schritt  string `json:"schritt"`
	Gelesen  int    `json:"gelesen"`
	Genommen int    `json:"genommen"`
	Begonnen string `json:"begonnen,omitempty"`
	Fertig   string `json:"fertig,omitempty"`
	Fehler   string `json:"fehler,omitempty"`
}

func (s *ImportStand) Kopie() ImportStand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ImportStand{Laeuft: s.Laeuft, Schritt: s.Schritt, Gelesen: s.Gelesen, Genommen: s.Genommen,
		Begonnen: s.Begonnen, Fertig: s.Fertig, Fehler: s.Fehler}
}

func (s *ImportStand) setze(f func(*ImportStand)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// StarteOFF liest Open Food Facts im Hintergrund ein. false, wenn schon ein
// Import laeuft.
func (k *Katalog) StarteOFF(adresse string) bool {
	k.Import.mu.Lock()
	if k.Import.Laeuft {
		k.Import.mu.Unlock()
		return false
	}
	k.Import.Laeuft, k.Import.Fehler, k.Import.Fertig = true, "", ""
	k.Import.Gelesen, k.Import.Genommen = 0, 0
	k.Import.Begonnen = time.Now().Format("2006-01-02 15:04")
	k.Import.Schritt = "lädt herunter"
	k.Import.mu.Unlock()
	go func() {
		err := k.importiereOFF(context.Background(), adresse)
		k.Import.setze(func(s *ImportStand) {
			s.Laeuft = false
			s.Fertig = time.Now().Format("2006-01-02 15:04")
			if err != nil {
				s.Fehler = err.Error()
				s.Schritt = "abgebrochen"
			} else {
				s.Schritt = "fertig"
			}
		})
		if err != nil {
			log.Printf("Open Food Facts: %v", err)
		}
	}()
	return true
}

// HalteOFFAktuell liest beim ersten Start und danach alle 30 Tage neu ein.
func (k *Katalog) HalteOFFAktuell(adresse string) {
	for {
		var wann sql.NullTime
		_ = k.db.QueryRow(`SELECT eingelesen FROM lm_quellen WHERE quelle = 'off'`).Scan(&wann)
		if !wann.Valid || time.Since(wann.Time) > 30*24*time.Hour {
			if k.StarteOFF(adresse) {
				log.Printf("Open Food Facts: Import gestartet")
			}
		}
		time.Sleep(6 * time.Hour)
	}
}

func (k *Katalog) importiereOFF(ctx context.Context, adresse string) error {
	anfrage, err := http.NewRequestWithContext(ctx, http.MethodGet, adresse, nil)
	if err != nil {
		return err
	}
	anfrage.Header.Set("User-Agent", "Foodr/1.0 (https://github.com/Dschonas04/foodr)")
	antwort, err := (&http.Client{Timeout: 3 * time.Hour}).Do(anfrage)
	if err != nil {
		return fmt.Errorf("Download: %w", err)
	}
	defer antwort.Body.Close()
	if antwort.StatusCode != http.StatusOK {
		return fmt.Errorf("Download: Antwort %d", antwort.StatusCode)
	}
	var r io.Reader = antwort.Body
	if strings.HasSuffix(adresse, ".gz") {
		gz, err := gzip.NewReader(antwort.Body)
		if err != nil {
			return err
		}
		r = gz
	}
	return k.leseOFF(r)
}

func (k *Katalog) leseOFF(r io.Reader) error {
	tsv := csv.NewReader(r)
	tsv.Comma = '\t'
	tsv.LazyQuotes = true
	tsv.FieldsPerRecord = -1
	tsv.ReuseRecord = true
	kopf, err := tsv.Read()
	if err != nil {
		return fmt.Errorf("Kopfzeile: %w", err)
	}
	spalte := map[string]int{}
	for i, n := range kopf {
		spalte[n] = i
	}
	for _, pflicht := range []string{"code", "product_name", "energy-kcal_100g"} {
		if _, ok := spalte[pflicht]; !ok {
			return fmt.Errorf("Spalte %s fehlt im Export", pflicht)
		}
	}

	if _, err := k.db.Exec(`DROP TABLE IF EXISTS produkte_neu; ` + produktTabelle("produkte_neu")); err != nil {
		return err
	}
	tx, err := k.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.Prepare(pq.CopyIn("produkte_neu", spalten...))
	if err != nil {
		return err
	}
	// Doppelte Barcodes kommen vor; gemerkt wird nur ein Fingerabdruck,
	// Millionen Zeichenketten wuerden Hunderte MB kosten.
	gesehen := map[uint64]struct{}{}
	k.Import.setze(func(s *ImportStand) { s.Schritt = "liest Produkte" })
	n, genommen := 0, 0
	for {
		z, err := tsv.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				continue // eine kaputte Zeile kippt nicht den ganzen Import
			}
			st.Close()
			return fmt.Errorf("Zeile %d: %w", n, err)
		}
		n++
		if lm := offZeile(z, spalte, k); lm != nil && neu(gesehen, lm.ID) {
			if _, err := st.Exec(lm.zeile()...); err != nil {
				st.Close()
				return fmt.Errorf("kopieren: %w", err)
			}
			genommen++
		}
		if n%20000 == 0 {
			k.Import.setze(func(s *ImportStand) { s.Gelesen, s.Genommen = n, genommen })
		}
	}
	if _, err := st.Exec(); err != nil {
		st.Close()
		return err
	}
	if err := st.Close(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	k.Import.setze(func(s *ImportStand) {
		s.Gelesen, s.Genommen = n, genommen
		s.Schritt = "baut Suchindex"
	})
	if genommen < 1000 {
		return fmt.Errorf("nur %d brauchbare Produkte, alter Stand bleibt", genommen)
	}
	if _, err := k.db.Exec(produktIndizes("produkte_neu") + "; ANALYZE produkte_neu"); err != nil {
		return fmt.Errorf("Index: %w", err)
	}

	k.Import.setze(func(s *ImportStand) { s.Schritt = "tauscht Tabellen" })
	tx, err = k.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tausch := []string{
		`DROP VIEW IF EXISTS alle_lebensmittel`,
		`DROP TABLE IF EXISTS produkte`,
		`ALTER TABLE produkte_neu RENAME TO produkte`,
		`ALTER INDEX produkte_neu_pkey RENAME TO produkte_pkey`,
	}
	for _, i := range []string{"such", "reihe", "gruppe", "barcode", "kcal", "eiweiss"} {
		tausch = append(tausch, fmt.Sprintf(`ALTER INDEX produkte_neu_%[1]s RENAME TO produkte_%[1]s`, i))
	}
	tausch = append(tausch, sichtAlle)
	for _, q := range tausch {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO lm_quellen (quelle, stand, anzahl, eingelesen) VALUES ('off',$1,$2,now())
		ON CONFLICT (quelle) DO UPDATE SET stand=EXCLUDED.stand, anzahl=EXCLUDED.anzahl, eingelesen=now()`,
		time.Now().Format("2006-01-02"), genommen); err != nil {
		return err
	}
	return tx.Commit()
}

// Open Food Facts gibt alles in Gramm je 100 g an; der BLS fuehrt Vitamine
// und Mineralstoffe in mg oder µg. umrechnen bringt die Werte auf die
// Einheit des Naehrstoffs.
var offNaehrstoffe = map[string]string{
	"energy-kj_100g": "ENERCJ", "energy-kcal_100g": "ENERCC", "proteins_100g": "PROT625",
	"fat_100g": "FAT", "saturated-fat_100g": "FASAT", "monounsaturated-fat_100g": "FAMS",
	"polyunsaturated-fat_100g": "FAPU", "omega-3-fat_100g": "FAPUN3", "omega-6-fat_100g": "FAPUN6",
	"cholesterol_100g": "CHORL", "carbohydrates_100g": "CHO", "sugars_100g": "SUGAR",
	"sucrose_100g": "SUCS", "glucose_100g": "GLUS", "fructose_100g": "FRUS", "lactose_100g": "LACS",
	"maltose_100g": "MALS", "starch_100g": "STARCH", "polyols_100g": "POLYL", "fiber_100g": "FIBT",
	"salt_100g": "NACL", "sodium_100g": "NA", "alcohol_100g": "ALC", "water_100g": "WATER",
	"vitamin-a_100g": "VITA", "beta-carotene_100g": "CARTB", "vitamin-d_100g": "VITD",
	"vitamin-e_100g": "VITE", "vitamin-k_100g": "VITK", "vitamin-c_100g": "VITC",
	"vitamin-b1_100g": "THIA", "vitamin-b2_100g": "RIBF", "vitamin-pp_100g": "NIA",
	"vitamin-b6_100g": "VITB6", "vitamin-b9_100g": "FOL", "vitamin-b12_100g": "VITB12",
	"biotin_100g": "BIOT", "pantothenic-acid_100g": "PANTAC", "potassium_100g": "K",
	"chloride_100g": "CLD", "calcium_100g": "CA", "phosphorus_100g": "P", "iron_100g": "FE",
	"magnesium_100g": "MG", "zinc_100g": "ZN", "copper_100g": "CU", "manganese_100g": "MN",
	"fluoride_100g": "FD", "chromium_100g": "CR", "molybdenum_100g": "MO", "iodine_100g": "ID",
}

var einheitFaktor = map[string]float64{"g": 1, "mg": 1e3, "µg": 1e6, "kcal": 1, "kJ": 1}

// Open-Food-Facts-Gruppen (pnns_groups_2) auf die Hauptgruppen.
var offGruppen = map[string]string{
	"Cheese": "M", "Milk and yogurt": "M", "Dairy desserts": "M", "Bread": "B", "Breakfast cereals": "C",
	"Cereals": "C", "Legumes": "H", "Nuts": "H", "Potatoes": "K", "Fruits": "F", "Dried fruits": "F",
	"Vegetables": "G", "Fish and seafood": "T", "Lean fish": "T", "Fatty fish": "T", "Meat": "U",
	"Processed meat": "W", "Eggs": "E", "Offals": "V", "Sweetened beverages": "N",
	"Unsweetened beverages": "N", "Fruit juices": "N", "Fruit nectars": "N",
	"Artificially sweetened beverages": "N", "Waters and flavored waters": "N",
	"Teas and herbal teas and coffees": "N", "Plant-based milk substitutes": "N",
	"Alcoholic beverages": "P", "Fats": "Q", "Dressings and sauces": "R", "Chocolate products": "S",
	"Sweets": "S", "Ice cream": "S", "Biscuits and cakes": "D", "Pastries": "D", "Appetizers": "Y",
	"Salty and fatty products": "Y", "One-dish meals": "Y", "Pizza pies and quiches": "Y",
	"Sandwiches": "Y", "Soups": "Y",
}

// offZeile macht aus einer Exportzeile ein Lebensmittel, oder nil, wenn
// Name oder Energie fehlen oder die Werte nicht stimmen koennen.
func offZeile(z []string, spalte map[string]int, k *Katalog) *Lebensmittel {
	feld := func(n string) string {
		if i, ok := spalte[n]; ok && i < len(z) {
			// Ein Nullbyte oder kaputtes UTF-8 wuerde das ganze COPY abbrechen.
			return strings.ToValidUTF8(strings.ReplaceAll(strings.TrimSpace(z[i]), "\x00", ""), "")
		}
		return ""
	}
	code := feld("code")
	name := feld("product_name")
	if name == "" {
		name = feld("generic_name")
	}
	if code == "" || name == "" || len(code) > 32 {
		return nil
	}
	lm := &Lebensmittel{ID: "off-" + code, Quelle: "off", Name: kuerze(name, 200), Barcode: code,
		Marke: kuerze(ersteVon(feld("brands")), 80), Kategorie: kuerze(feld("main_category_en"), 120),
		Menge: kuerze(feld("quantity"), 60), Nutriscore: strings.ToUpper(feld("nutriscore_grade")),
		Zutaten: kuerze(feld("ingredients_text"), 4000), Allergene: kuerze(liste(feld("allergens_en")), 300),
		Labels: kuerze(liste(feld("labels_en")), 300), Laender: kuerze(liste(feld("countries_en")), 300),
		Bild: feld("image_small_url"), Gruppe: offGruppen[feld("pnns_groups_2")], Werte: map[string]float64{}}
	if !strings.ContainsRune("ABCDE", rune(firstByte(lm.Nutriscore))) || len(lm.Nutriscore) != 1 {
		lm.Nutriscore = ""
	}
	if !strings.HasPrefix(lm.Bild, "https://") {
		lm.Bild = ""
	}
	if n, err := strconv.Atoi(feld("nova_group")); err == nil && n >= 1 && n <= 4 {
		lm.Nova = n
	}
	if p, err := strconv.ParseFloat(feld("serving_quantity"), 64); err == nil && p > 0 && p < 5000 {
		lm.PortionG = p
	}
	if n, err := strconv.Atoi(feld("unique_scans_n")); err == nil && n > 0 {
		lm.Beliebt = n
	}
	laender := feld("countries_tags")
	lm.DACH = strings.Contains(laender, "en:germany") || strings.Contains(laender, "en:austria") ||
		strings.Contains(laender, "en:switzerland")

	for spaltenName, stoff := range offNaehrstoffe {
		roh := feld(spaltenName)
		if roh == "" {
			continue
		}
		v, err := strconv.ParseFloat(roh, 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		i, ok := k.stelle[stoff]
		if !ok {
			continue
		}
		f, ok := einheitFaktor[k.stoffe[i].Einheit]
		if !ok {
			continue
		}
		if v*f < 1e-9 {
			v = 0
		}
		lm.Werte[stoff] = rundeSig(v * f)
	}
	if _, ok := lm.Werte["ENERCC"]; !ok {
		if kj, ok := lm.Werte["ENERCJ"]; ok {
			lm.Werte["ENERCC"] = rundeSig(kj / 4.184)
		}
	}
	kcal, ok := lm.Werte["ENERCC"]
	if !ok || kcal > 950 {
		return nil
	}
	summe := 0.0
	for _, c := range []string{"PROT625", "FAT", "CHO", "FIBT", "NACL", "ALC"} {
		v := lm.Werte[c]
		if v > 100 {
			return nil
		}
		summe += v
	}
	if summe > 105 || lm.Werte["SUGAR"] > 100 || lm.Werte["FASAT"] > 100 {
		return nil
	}
	return lm
}

// liste macht aus "en:milk,nuts" ein lesbares "milk, nuts".
func liste(s string) string {
	teile := strings.Split(s, ",")
	raus := teile[:0]
	for _, t := range teile {
		t = strings.TrimSpace(t)
		if i := strings.Index(t, ":"); i >= 0 && i <= 3 {
			t = t[i+1:]
		}
		if t != "" {
			raus = append(raus, t)
		}
	}
	return strings.Join(raus, ", ")
}

func ersteVon(liste string) string {
	if i := strings.IndexByte(liste, ','); i >= 0 {
		return strings.TrimSpace(liste[:i])
	}
	return liste
}

func firstByte(s string) byte {
	if s == "" {
		return 0
	}
	return s[0]
}

func kuerze(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// rundeSig kuerzt auf vier signifikante Stellen, wie der BLS.
func rundeSig(v float64) float64 {
	z, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 4, 64), 64)
	return z
}
