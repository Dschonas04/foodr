package main

import (
	"bufio"
	"compress/gzip"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/lib/pq"
)

// Die Lebensmitteldatenbank liegt in PostgreSQL: der Bundeslebensmittel-
// schluessel (BLS 4.0, 7.140 Grundlebensmittel mit 138 Naehrstoffen) und die
// eigenen Lebensmittel in der Tabelle lebensmittel, die Markenprodukte aus
// Open Food Facts (einige Millionen) in der Tabelle produkte. Die Sicht
// alle_lebensmittel legt beide zusammen.

//go:embed daten/naehrstoffe.tsv daten/bls.tsv.gz
var blsDateien embed.FS

// Steigt, wenn sich daten/bls.tsv.gz oder die Aufbereitung aendert; dann
// wird der BLS beim Start neu eingelesen.
const blsStand = "4.0/2"

// Spuren unter der Nachweisgrenze stehen in den Werten als -1.
const spurWert = -1

type Naehrstoff struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	NameEN  string `json:"name_en"`
	Einheit string `json:"einheit"`
	Gruppe  string `json:"gruppe"`
}

type Lebensmittel struct {
	ID         string             `json:"id"`
	Quelle     string             `json:"quelle"` // bls, off, eigen
	Name       string             `json:"name"`
	NameEN     string             `json:"name_en,omitempty"`
	Marke      string             `json:"marke,omitempty"`
	Gruppe     string             `json:"gruppe"` // Hauptgruppe, z. B. "G" fuer Gemuese
	Kategorie  string             `json:"kategorie,omitempty"`
	Barcode    string             `json:"barcode,omitempty"`
	Menge      string             `json:"menge,omitempty"`
	PortionG   float64            `json:"portion_g,omitempty"`
	StueckG    float64            `json:"stueck_g,omitempty"`
	Nutriscore string             `json:"nutriscore,omitempty"`
	Nova       int                `json:"nova,omitempty"`
	Zutaten    string             `json:"zutaten,omitempty"`
	Allergene  string             `json:"allergene,omitempty"`
	Labels     string             `json:"labels,omitempty"`
	Laender    string             `json:"laender,omitempty"`
	Bild       string             `json:"bild,omitempty"`
	Beliebt    int                `json:"beliebt,omitempty"`
	DACH       bool               `json:"dach,omitempty"`
	Werte      map[string]float64 `json:"-"`
}

// Hauptgruppen nach dem ersten Buchstaben des BLS-Schluessels. Markenprodukte
// werden ueber ihre Open-Food-Facts-Gruppe derselben Einteilung zugeschlagen.
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
	{"Y", "Gerichte, Snacks und Desserts"},
}

var Quellen = []struct{ Code, Name, Lizenz, Adresse string }{
	{"bls", "Bundeslebensmittelschlüssel 4.0 (Max Rubner-Institut)", "CC BY 4.0",
		"https://doi.org/10.25826/Data20251217-134202-0"},
	{"off", "Open Food Facts", "ODbL 1.0", "https://world.openfoodfacts.org"},
	{"eigen", "Eigene Lebensmittel", "", ""},
}

func kategorieName(code string) string {
	for _, k := range Kategorien {
		if k.Code == code {
			return k.Name
		}
	}
	return ""
}

// Die Werte, die an Rezepten, Plaenen und Listen stehen. Sie haben eigene
// Spalten; alles andere steht in werte und in der Detailansicht.
var Kernwerte = []string{"ENERCC", "PROT625", "FAT", "FASAT", "CHO", "SUGAR", "FIBT", "NACL"}

type Katalog struct {
	db     *sql.DB
	stoffe []Naehrstoff
	stelle map[string]int
	Import *ImportStand
}

// neuerKatalog kennt die Naehrstoffe, aber noch keine Datenbank.
func neuerKatalog() (*Katalog, error) {
	k := &Katalog{stelle: map[string]int{}, Import: &ImportStand{}}
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
	return k, nil
}

func LadeKatalog(db *sql.DB) (*Katalog, error) {
	k, err := neuerKatalog()
	if err != nil {
		return nil, err
	}
	k.db = db
	if err := migriereAlteEigene(db); err != nil {
		return nil, fmt.Errorf("eigene Lebensmittel übernehmen: %w", err)
	}
	for _, t := range []string{"lebensmittel", "produkte"} {
		if _, err := db.Exec(produktTabelle(t) + "; " + produktIndizes(t)); err != nil {
			return nil, fmt.Errorf("Tabelle %s: %w", t, err)
		}
	}
	if _, err := db.Exec(sichtAlle); err != nil {
		return nil, fmt.Errorf("Sicht: %w", err)
	}
	if err := k.pflegeNaehrstoffe(); err != nil {
		return nil, err
	}
	if err := k.pflegeBLS(); err != nil {
		return nil, err
	}
	return k, nil
}

func (k *Katalog) pflegeNaehrstoffe() error {
	tx, err := k.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, s := range k.stoffe {
		if _, err := tx.Exec(`INSERT INTO naehrstoffe (code, name, name_en, einheit, gruppe, reihe)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name,
			name_en=EXCLUDED.name_en, einheit=EXCLUDED.einheit, gruppe=EXCLUDED.gruppe, reihe=EXCLUDED.reihe`,
			s.Code, s.Name, s.NameEN, s.Einheit, s.Gruppe, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// pflegeBLS liest den eingebetteten BLS ein, wenn er fehlt oder veraltet ist.
func (k *Katalog) pflegeBLS() error {
	var stand string
	_ = k.db.QueryRow(`SELECT stand FROM lm_quellen WHERE quelle = 'bls'`).Scan(&stand)
	if stand == blsStand {
		return nil
	}
	datei, err := blsDateien.Open("daten/bls.tsv.gz")
	if err != nil {
		return err
	}
	defer datei.Close()
	gz, err := gzip.NewReader(datei)
	if err != nil {
		return err
	}
	liste, err := k.leseBLS(gz)
	if err != nil {
		return err
	}
	tx, err := k.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM lebensmittel WHERE quelle = 'bls'`); err != nil {
		return err
	}
	if err := kopiere(tx, "lebensmittel", liste); err != nil {
		return fmt.Errorf("BLS einlesen: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO lm_quellen (quelle, stand, anzahl, eingelesen) VALUES ('bls',$1,$2,now())
		ON CONFLICT (quelle) DO UPDATE SET stand=EXCLUDED.stand, anzahl=EXCLUDED.anzahl, eingelesen=now()`,
		blsStand, len(liste)); err != nil {
		return err
	}
	return tx.Commit()
}

func (k *Katalog) leseBLS(r io.Reader) ([]*Lebensmittel, error) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	var liste []*Lebensmittel
	for s.Scan() {
		f := strings.Split(s.Text(), "\t")
		if len(f) != 3+len(k.stoffe) {
			return nil, fmt.Errorf("bls.tsv: %d statt %d Spalten bei %q", len(f), 3+len(k.stoffe), f[0])
		}
		lm := &Lebensmittel{ID: f[0], Quelle: "bls", Name: f[1], NameEN: f[2], Gruppe: f[0][:1],
			Werte: map[string]float64{}}
		for i, v := range f[3:] {
			switch v {
			case "":
			case "~":
				lm.Werte[k.stoffe[i].Code] = spurWert
			default:
				z, err := strconv.ParseFloat(v, 64)
				if err != nil {
					return nil, fmt.Errorf("bls.tsv: %s/%s: %w", f[0], k.stoffe[i].Code, err)
				}
				lm.Werte[k.stoffe[i].Code] = z
			}
		}
		liste = append(liste, lm)
	}
	return liste, s.Err()
}

// ---------- Tabellen ----------

// Beide Tabellen haben dieselben Spalten, damit die Sicht sie einfach
// aneinanderhaengen kann. produkte wird beim Import neu gebaut und getauscht.
func produktTabelle(name string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %[1]s (
    id         TEXT PRIMARY KEY,
    quelle     TEXT NOT NULL,
    rang       SMALLINT NOT NULL DEFAULT 2,
    name       TEXT NOT NULL,
    name_en    TEXT NOT NULL DEFAULT '',
    marke      TEXT NOT NULL DEFAULT '',
    gruppe     TEXT NOT NULL DEFAULT '',
    kategorie  TEXT NOT NULL DEFAULT '',
    barcode    TEXT,
    menge      TEXT NOT NULL DEFAULT '',
    portion_g  REAL,
    stueck_g   REAL,
    nutriscore TEXT NOT NULL DEFAULT '',
    nova       SMALLINT,
    zutaten    TEXT NOT NULL DEFAULT '',
    allergene  TEXT NOT NULL DEFAULT '',
    labels     TEXT NOT NULL DEFAULT '',
    laender    TEXT NOT NULL DEFAULT '',
    bild       TEXT NOT NULL DEFAULT '',
    beliebt    INT NOT NULL DEFAULT 0,
    dach       BOOLEAN NOT NULL DEFAULT false,
    such       TEXT NOT NULL,
    kcal REAL, eiweiss REAL, fett REAL, ges_fett REAL, kh REAL, zucker REAL, ballast REAL, salz REAL,
    werte      JSONB NOT NULL DEFAULT '{}'
)`, name)
}

// Die Indizes kommen erst nach dem Einlesen dazu; Millionen Zeilen in eine
// Tabelle mit Trigramm-Index zu schreiben, dauert ein Vielfaches.
func produktIndizes(name string) string {
	return fmt.Sprintf(`
CREATE INDEX IF NOT EXISTS %[1]s_such ON %[1]s USING gin (such gin_trgm_ops);
CREATE INDEX IF NOT EXISTS %[1]s_reihe ON %[1]s (rang, beliebt DESC, id);
CREATE INDEX IF NOT EXISTS %[1]s_gruppe ON %[1]s (gruppe, rang, beliebt DESC);
CREATE INDEX IF NOT EXISTS %[1]s_barcode ON %[1]s (barcode);
CREATE INDEX IF NOT EXISTS %[1]s_kcal ON %[1]s (kcal);
CREATE INDEX IF NOT EXISTS %[1]s_eiweiss ON %[1]s (eiweiss DESC)`, name)
}

const sichtAlle = `CREATE OR REPLACE VIEW alle_lebensmittel AS
    SELECT * FROM lebensmittel UNION ALL SELECT * FROM produkte`

var spalten = []string{"id", "quelle", "rang", "name", "name_en", "marke", "gruppe", "kategorie", "barcode",
	"menge", "portion_g", "stueck_g", "nutriscore", "nova", "zutaten", "allergene", "labels", "laender",
	"bild", "beliebt", "dach", "such", "kcal", "eiweiss", "fett", "ges_fett", "kh", "zucker", "ballast",
	"salz", "werte"}

var alleSpalten = strings.Join(spalten, ", ")

func rangVon(quelle string) int {
	switch quelle {
	case "eigen":
		return 0
	case "bls":
		return 1
	}
	return 2
}

// zeile bringt ein Lebensmittel in die Reihenfolge von spalten.
func (lm *Lebensmittel) zeile() []any {
	werte, _ := json.Marshal(lm.Werte)
	leer := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	null := func(f float64) any {
		if f <= 0 {
			return nil
		}
		return f
	}
	z := []any{lm.ID, lm.Quelle, rangVon(lm.Quelle), lm.Name, lm.NameEN, lm.Marke, lm.Gruppe, lm.Kategorie,
		leer(lm.Barcode), lm.Menge, null(lm.PortionG), null(lm.StueckG), lm.Nutriscore, null(float64(lm.Nova)),
		lm.Zutaten, lm.Allergene, lm.Labels, lm.Laender, lm.Bild, lm.Beliebt, lm.DACH, suchText(lm)}
	for _, c := range Kernwerte {
		if v, ok := lm.Werte[c]; ok {
			// Spuren und Werte unterhalb dessen, was REAL fasst, zaehlen als 0.
			if v < 1e-9 {
				v = 0
			}
			z = append(z, v)
		} else {
			z = append(z, nil)
		}
	}
	return append(z, string(werte))
}

func kopiere(tx *sql.Tx, tabelle string, liste []*Lebensmittel) error {
	st, err := tx.Prepare(pq.CopyIn(tabelle, spalten...))
	if err != nil {
		return err
	}
	for _, lm := range liste {
		if _, err := st.Exec(lm.zeile()...); err != nil {
			st.Close()
			return err
		}
	}
	if _, err := st.Exec(); err != nil {
		st.Close()
		return err
	}
	return st.Close()
}

// Bis Oktober 2026 hatte lebensmittel nur eigene Eintraege mit Zahlen als
// Kennung. Die werden in die neue Tabelle uebernommen.
func migriereAlteEigene(db *sql.DB) error {
	var typ string
	err := db.QueryRow(`SELECT data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'lebensmittel' AND column_name = 'id'`).Scan(&typ)
	if errors.Is(err, sql.ErrNoRows) || typ != "integer" {
		return nil
	}
	if err != nil {
		return err
	}
	zeilen, err := db.Query(`SELECT id, name, stueck_g, werte FROM lebensmittel`)
	if err != nil {
		return err
	}
	var alte []*Lebensmittel
	hoechste := 0
	for zeilen.Next() {
		var n int
		var werte []byte
		lm := &Lebensmittel{Quelle: "eigen"}
		if err := zeilen.Scan(&n, &lm.Name, &lm.StueckG, &werte); err != nil {
			zeilen.Close()
			return err
		}
		_ = json.Unmarshal(werte, &lm.Werte)
		lm.ID = eigeneID(n)
		if n > hoechste {
			hoechste = n
		}
		alte = append(alte, lm)
	}
	zeilen.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DROP TABLE lebensmittel`, produktTabelle("lebensmittel")} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	if err := kopiere(tx, "lebensmittel", alte); err != nil {
		return err
	}
	if hoechste > 0 {
		if _, err := tx.Exec(`SELECT setval('eigen_nr', $1)`, hoechste); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------- Lesen ----------

func leseLebensmittel(s interface{ Scan(...any) error }) (*Lebensmittel, error) {
	var lm Lebensmittel
	var barcode sql.NullString
	var portion, stueck sql.NullFloat64
	var nova sql.NullInt64
	var rang int
	var such string
	kern := make([]sql.NullFloat64, len(Kernwerte))
	var werte []byte
	ziele := []any{&lm.ID, &lm.Quelle, &rang, &lm.Name, &lm.NameEN, &lm.Marke, &lm.Gruppe, &lm.Kategorie,
		&barcode, &lm.Menge, &portion, &stueck, &lm.Nutriscore, &nova, &lm.Zutaten, &lm.Allergene, &lm.Labels,
		&lm.Laender, &lm.Bild, &lm.Beliebt, &lm.DACH, &such}
	for i := range kern {
		ziele = append(ziele, &kern[i])
	}
	ziele = append(ziele, &werte)
	if err := s.Scan(ziele...); err != nil {
		return nil, err
	}
	lm.Barcode, lm.PortionG, lm.StueckG, lm.Nova = barcode.String, portion.Float64, stueck.Float64, int(nova.Int64)
	if err := json.Unmarshal(werte, &lm.Werte); err != nil {
		return nil, err
	}
	if lm.Werte == nil {
		lm.Werte = map[string]float64{}
	}
	return &lm, nil
}

// Hole liefert ein Lebensmittel oder nil, wenn es das nicht gibt.
func (k *Katalog) Hole(id string) *Lebensmittel {
	tabelle := "lebensmittel"
	if strings.HasPrefix(id, "off-") {
		tabelle = "produkte"
	}
	lm, err := leseLebensmittel(k.db.QueryRow(`SELECT `+alleSpalten+` FROM `+tabelle+` WHERE id = $1`, id))
	if err != nil {
		return nil
	}
	return lm
}

// Wert je 100 g; ok=false, wenn er nicht bestimmt ist. Spuren zaehlen als 0.
func (k *Katalog) Wert(lm *Lebensmittel, code string) (float64, bool) {
	v, ok := lm.Werte[code]
	if !ok {
		return 0, false
	}
	if v < 0 {
		return 0, true
	}
	return v, true
}

type Bestand struct {
	Quelle     string `json:"quelle"`
	Name       string `json:"name"`
	Lizenz     string `json:"lizenz"`
	Adresse    string `json:"adresse"`
	Anzahl     int    `json:"anzahl"`
	Stand      string `json:"stand"`
	Eingelesen string `json:"eingelesen"`
}

// Bestaende zaehlt je Quelle; die grossen Zahlen kommen aus lm_quellen, damit
// niemand Millionen Zeilen zaehlen muss.
func (k *Katalog) Bestaende() []Bestand {
	raus := []Bestand{}
	for _, q := range Quellen {
		b := Bestand{Quelle: q.Code, Name: q.Name, Lizenz: q.Lizenz, Adresse: q.Adresse}
		if q.Code == "eigen" {
			_ = k.db.QueryRow(`SELECT count(*) FROM lebensmittel WHERE quelle = 'eigen'`).Scan(&b.Anzahl)
		} else {
			var wann sql.NullTime
			_ = k.db.QueryRow(`SELECT stand, anzahl, eingelesen FROM lm_quellen WHERE quelle = $1`, q.Code).
				Scan(&b.Stand, &b.Anzahl, &wann)
			if wann.Valid {
				b.Eingelesen = wann.Time.Format("2006-01-02 15:04")
			}
		}
		raus = append(raus, b)
	}
	return raus
}

func (k *Katalog) Anzahl() int {
	n := 0
	for _, b := range k.Bestaende() {
		n += b.Anzahl
	}
	return n
}

// ---------- Suche ----------

type Frage struct {
	Text   string
	Quelle string // bls, off, eigen oder leer
	Gruppe string
	DACH   bool   // Markenprodukte nur aus Deutschland, Oesterreich, der Schweiz
	Ordnen string // leer (Relevanz), kcal, kcal_ab, eiweiss, name
	Grenze int
	Ab     int
}

var nurZiffern = regexp.MustCompile(`^\d{6,14}$`)

var likeMaske = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// Suche findet Lebensmittel, deren Name oder Marke jedes Suchwort enthaelt.
// Eigene und BLS-Lebensmittel stehen vor Markenprodukten, beliebte und
// hiesige Produkte vor dem Rest. gesamt ist bei 10.001 gedeckelt.
func (k *Katalog) Suche(f Frage) (liste []*Lebensmittel, gesamt int, err error) {
	if f.Grenze <= 0 || f.Grenze > 200 {
		f.Grenze = 50
	}
	if f.Ab < 0 {
		f.Ab = 0
	}
	var bed []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	text := strings.TrimSpace(f.Text)
	// Einzelne Buchstaben treffen Millionen Zeilen und helfen niemandem.
	var woerter []string
	for _, w := range strings.Fields(falte(text)) {
		if len(w) >= 2 {
			woerter = append(woerter, w)
		}
	}
	if nurZiffern.MatchString(text) {
		bed = append(bed, "barcode = "+arg(text))
		woerter = nil
	}
	for _, w := range woerter {
		bed = append(bed, "such LIKE "+arg("%"+likeMaske.Replace(w)+"%"))
	}
	if f.Quelle != "" {
		bed = append(bed, "quelle = "+arg(f.Quelle))
	}
	if f.Gruppe != "" {
		bed = append(bed, "gruppe = "+arg(f.Gruppe))
	}
	if f.DACH {
		bed = append(bed, "(dach OR quelle <> 'off')")
	}
	quelle := "alle_lebensmittel"
	switch f.Quelle {
	case "off":
		quelle = "produkte"
	case "bls", "eigen":
		quelle = "lebensmittel"
	}
	wo := ""
	if len(bed) > 0 {
		wo = " WHERE " + strings.Join(bed, " AND ")
	}
	zaehlArgs := append([]any(nil), args...)

	ordnung := "rang, beliebt DESC, id"
	switch f.Ordnen {
	case "kcal":
		ordnung = "kcal NULLS LAST, rang, id"
	case "kcal_ab":
		ordnung = "kcal DESC NULLS LAST, rang, id"
	case "eiweiss":
		ordnung = "eiweiss DESC NULLS LAST, rang, id"
	case "name":
		ordnung = "name, id"
	default:
		if len(woerter) > 0 {
			ordnung = fmt.Sprintf(`(CASE rang WHEN 0 THEN 60 WHEN 1 THEN 45 ELSE 0 END)
				+ similarity(such, %[1]s) * 40
				+ (CASE WHEN such LIKE %[2]s THEN 6 ELSE 0 END)
				+ (CASE WHEN such LIKE %[3]s THEN 15 ELSE 0 END)
				+ (CASE WHEN such LIKE %[4]s THEN 12 ELSE 0 END)
				+ (CASE WHEN dach THEN 8 ELSE 0 END)
				+ least(ln(beliebt + 1), 8)
				+ (CASE WHEN such LIKE '%% roh %%' THEN 5 ELSE 0 END)
				- (CASE WHEN gruppe IN ('X','Y','D') THEN 8 ELSE 0 END)
				- length(name) / 12.0 DESC, id`,
				arg(strings.Join(woerter, " ")), arg(" "+likeMaske.Replace(woerter[0])+"%"),
				arg(" "+likeMaske.Replace(woerter[0])+" %"), arg("% "+likeMaske.Replace(woerter[0])+" %"))
		}
	}

	zeilen, err := k.db.Query(`SELECT `+alleSpalten+` FROM `+quelle+wo+` ORDER BY `+ordnung+
		` LIMIT `+arg(f.Grenze)+` OFFSET `+arg(f.Ab), args...)
	if err != nil {
		return nil, 0, err
	}
	defer zeilen.Close()
	for zeilen.Next() {
		lm, err := leseLebensmittel(zeilen)
		if err != nil {
			return nil, 0, err
		}
		liste = append(liste, lm)
	}
	if err := zeilen.Err(); err != nil {
		return nil, 0, err
	}
	err = k.db.QueryRow(`SELECT count(*) FROM (SELECT 1 FROM `+quelle+wo+` LIMIT 10001) t`, zaehlArgs...).Scan(&gesamt)
	return liste, gesamt, err
}

// ---------- Eigene Lebensmittel ----------

func (k *Katalog) SpeichereEigenes(lm *Lebensmittel) (string, error) {
	lm.Name = strings.TrimSpace(lm.Name)
	if lm.Name == "" {
		return "", errors.New("Name fehlt")
	}
	lm.Quelle = "eigen"
	for code, v := range lm.Werte {
		if _, bekannt := k.stelle[code]; !bekannt || v < 0 {
			delete(lm.Werte, code)
		}
	}
	if lm.Werte == nil {
		lm.Werte = map[string]float64{}
	}
	if lm.ID == "" {
		var n int
		if err := k.db.QueryRow(`SELECT nextval('eigen_nr')`).Scan(&n); err != nil {
			return "", err
		}
		lm.ID = eigeneID(n)
	} else if !strings.HasPrefix(lm.ID, "eigen-") {
		return "", errors.New("nur eigene Lebensmittel lassen sich ändern")
	}
	platz := []string{}
	setze := []string{}
	for i, s := range spalten {
		platz = append(platz, "$"+strconv.Itoa(i+1))
		if s != "id" {
			setze = append(setze, s+" = EXCLUDED."+s)
		}
	}
	_, err := k.db.Exec(`INSERT INTO lebensmittel (`+alleSpalten+`) VALUES (`+strings.Join(platz, ",")+`)
		ON CONFLICT (id) DO UPDATE SET `+strings.Join(setze, ", "), lm.zeile()...)
	return lm.ID, err
}

func (k *Katalog) LoescheEigenes(id string) error {
	if !strings.HasPrefix(id, "eigen-") {
		return errors.New("nur eigene Lebensmittel lassen sich löschen")
	}
	_, err := k.db.Exec(`DELETE FROM lebensmittel WHERE id = $1`, id)
	return err
}

func eigeneID(n int) string { return "eigen-" + strconv.Itoa(n) }

// ---------- Text ----------

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

// suchText ist, was die Suche durchsucht: Name, derselbe zusammengeschrieben
// (der BLS schreibt "Hafer Flocken") und die Marke, mit Leerzeichen an den
// Raendern, damit " wort%" einen Wortanfang trifft.
func suchText(lm *Lebensmittel) string {
	n := falte(lm.Name)
	t := " " + n + " "
	if z := strings.ReplaceAll(n, " ", ""); z != n {
		t += z + " "
	}
	if m := falte(lm.Marke); m != "" {
		t += m + " "
	}
	return t
}

// bewerteTreffer ordnet Kandidaten fuer die automatische Zuordnung von
// Zutaten: was mit dem ersten Wort beginnt, roh ist und kurz heisst, steht vorne.
func bewerteTreffer(lm *Lebensmittel, woerter []string) (float64, bool) {
	such := falte(lm.Name)
	zusammen := strings.ReplaceAll(such, " ", "")
	if len(woerter) == 0 {
		return -float64(len(such)) / 10, true
	}
	p := 0.0
	namensWoerter := strings.Fields(such)
	for _, w := range woerter {
		if !strings.Contains(such, w) {
			if !strings.Contains(zusammen, w) {
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
	if strings.HasPrefix(zusammen, woerter[0]) {
		p += 25
	}
	if strings.Contains(" "+such+" ", " roh ") {
		p += 12
	}
	if lm.Quelle == "eigen" {
		p += 15
	}
	switch lm.Gruppe {
	case "X", "Y", "D":
		p -= 20
	}
	if strings.Contains(such, "tiefgefroren") {
		p -= 6
	}
	p -= float64(len(such)) / 4
	return p, true
}
