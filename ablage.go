package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

type Ablage struct{ db *sql.DB }

func OeffneAblage(url, schema string) (*Ablage, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	// Die Datenbank kommt im Verbund oft ein paar Sekunden spaeter als wir.
	var letzter error
	for i := 0; i < 30; i++ {
		if letzter = db.Ping(); letzter == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if letzter != nil {
		return nil, fmt.Errorf("Datenbank nicht erreichbar: %w", letzter)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("Schema: %w", err)
	}
	return &Ablage{db: db}, nil
}

// ---------- Rezepte ----------

const rezeptSpalten = `id, titel, beschreibung, portionen, dauer_min, art, kost, aufwand,
	saison, etiketten, zutaten, schritte, hinweise, quelle, bewertung, zuletzt_gekocht`

func leseRezept(s interface {
	Scan(...any) error
}) (Rezept, error) {
	var r Rezept
	var zutaten, schritte []byte
	var gekocht sql.NullTime
	err := s.Scan(&r.ID, &r.Titel, &r.Beschreibung, &r.Portionen, &r.DauerMin, &r.Art, &r.Kost,
		&r.Aufwand, pq.Array((*[]string)(&r.Saison)), pq.Array((*[]string)(&r.Etiketten)), &zutaten, &schritte,
		&r.Hinweise, &r.Quelle, &r.Bewertung, &gekocht)
	if err != nil {
		return r, err
	}
	_ = json.Unmarshal(zutaten, &r.Zutaten)
	_ = json.Unmarshal(schritte, &r.Schritte)
	if gekocht.Valid {
		r.ZuletztGekocht = gekocht.Time.Format("2006-01-02")
	}
	if r.Zutaten == nil {
		r.Zutaten = []Zutat{}
	}
	if r.Schritte == nil {
		r.Schritte = []string{}
	}
	return r, nil
}

func (a *Ablage) Rezepte() ([]Rezept, error) {
	zeilen, err := a.db.Query(`SELECT ` + rezeptSpalten + ` FROM rezepte ORDER BY titel`)
	if err != nil {
		return nil, err
	}
	defer zeilen.Close()
	liste := []Rezept{}
	for zeilen.Next() {
		r, err := leseRezept(zeilen)
		if err != nil {
			return nil, err
		}
		liste = append(liste, r)
	}
	return liste, zeilen.Err()
}

func (a *Ablage) Rezept(id int) (Rezept, error) {
	return leseRezept(a.db.QueryRow(`SELECT `+rezeptSpalten+` FROM rezepte WHERE id = $1`, id))
}

func (a *Ablage) SpeichereRezept(r Rezept) (Rezept, error) {
	if strings.TrimSpace(r.Titel) == "" {
		return r, fmt.Errorf("ohne Titel geht es nicht")
	}
	ordne(&r)
	zutaten, _ := json.Marshal(r.Zutaten)
	schritte, _ := json.Marshal(r.Schritte)
	var gekocht any
	if r.ZuletztGekocht != "" {
		gekocht = r.ZuletztGekocht
	}
	werte := []any{r.Titel, r.Beschreibung, r.Portionen, r.DauerMin, r.Art, r.Kost, r.Aufwand,
		pq.Array([]string(r.Saison)), pq.Array([]string(r.Etiketten)), zutaten, schritte, r.Hinweise, r.Quelle,
		r.Bewertung, gekocht}
	if r.ID == 0 {
		return leseRezept(a.db.QueryRow(`INSERT INTO rezepte
			(titel, beschreibung, portionen, dauer_min, art, kost, aufwand, saison, etiketten,
			 zutaten, schritte, hinweise, quelle, bewertung, zuletzt_gekocht)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			RETURNING `+rezeptSpalten, werte...))
	}
	return leseRezept(a.db.QueryRow(`UPDATE rezepte SET titel=$1, beschreibung=$2, portionen=$3,
		dauer_min=$4, art=$5, kost=$6, aufwand=$7, saison=$8, etiketten=$9, zutaten=$10,
		schritte=$11, hinweise=$12, quelle=$13, bewertung=$14, zuletzt_gekocht=$15
		WHERE id=$16 RETURNING `+rezeptSpalten, append(werte, r.ID)...))
}

// Fuellt fehlende Angaben mit brauchbaren Vorgaben, damit die KI und die
// Oberflaeche nie auf leere Felder stossen.
func ordne(r *Rezept) {
	r.Art = einerVon(r.Art, "hauptgericht", "beilage", "fruehstueck", "nachtisch")
	r.Kost = einerVon(r.Kost, "vegan", "vegetarisch", "fisch", "fleisch")
	r.Aufwand = einerVon(r.Aufwand, "schnell", "mittel", "aufwendig")
	if r.Portionen <= 0 {
		r.Portionen = 2
	}
	if r.DauerMin <= 0 {
		r.DauerMin = 30
	}
	if r.Art == "" {
		r.Art = "hauptgericht"
	}
	if r.Kost == "" {
		r.Kost = "fleisch"
	}
	if r.Aufwand == "" {
		switch {
		case r.DauerMin <= 25:
			r.Aufwand = "schnell"
		case r.DauerMin <= 60:
			r.Aufwand = "mittel"
		default:
			r.Aufwand = "aufwendig"
		}
	}
	if r.Saison == nil {
		r.Saison = Liste{}
	}
	if r.Etiketten == nil {
		r.Etiketten = Liste{}
	}
	r.Kost = kostAusZutaten(r.Zutaten, r.Kost)
	saubere := make([]Zutat, 0, len(r.Zutaten))
	for i := range r.Zutaten {
		z := r.Zutaten[i]
		raeumeZutat(&z)
		if z.Name == "" {
			continue
		}
		saubere = append(saubere, z)
	}
	r.Zutaten = saubere
}

func (a *Ablage) LoescheRezept(id int) error {
	_, err := a.db.Exec(`DELETE FROM rezepte WHERE id = $1`, id)
	return err
}

// ---------- Plaene ----------

func (a *Ablage) PlanDerWoche(woche string) (*Plan, error) {
	var p Plan
	var erzeugt time.Time
	var w time.Time
	err := a.db.QueryRow(`SELECT id, woche, personen, modell, begruendung, erzeugt
		FROM plaene WHERE woche = $1`, woche).Scan(&p.ID, &w, &p.Personen, &p.Modell, &p.Begruendung, &erzeugt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Woche = w.Format("2006-01-02")
	p.Erzeugt = erzeugt.Format(time.RFC3339)
	p.Tage, err = a.PlanTage(p.ID)
	return &p, err
}

func (a *Ablage) PlanTage(planID int) ([]PlanTag, error) {
	zeilen, err := a.db.Query(`SELECT t.id, t.tag, COALESCE(t.rezept_id, 0), t.hinweis, t.gekocht
		FROM plan_tage t WHERE t.plan_id = $1 ORDER BY t.tag`, planID)
	if err != nil {
		return nil, err
	}
	defer zeilen.Close()
	tage := []PlanTag{}
	for zeilen.Next() {
		var t PlanTag
		var tag time.Time
		if err := zeilen.Scan(&t.ID, &tag, &t.RezeptID, &t.Hinweis, &t.Gekocht); err != nil {
			return nil, err
		}
		t.Tag = tag.Format("2006-01-02")
		if t.RezeptID != 0 {
			if r, err := a.Rezept(t.RezeptID); err == nil {
				t.Rezept = &r
			}
		}
		tage = append(tage, t)
	}
	return tage, zeilen.Err()
}

// Schreibt einen Plan samt Tagen; ein vorhandener Plan derselben Woche wird ersetzt.
func (a *Ablage) SchreibePlan(p Plan) (*Plan, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int
	err = tx.QueryRow(`INSERT INTO plaene (woche, personen, modell, begruendung)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (woche) DO UPDATE SET personen = EXCLUDED.personen,
			modell = EXCLUDED.modell, begruendung = EXCLUDED.begruendung, erzeugt = now()
		RETURNING id`, p.Woche, p.Personen, p.Modell, p.Begruendung).Scan(&id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM plan_tage WHERE plan_id = $1`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM einkauf WHERE plan_id = $1 AND NOT zusatz`, id); err != nil {
		return nil, err
	}
	for _, t := range p.Tage {
		var rez any
		if t.RezeptID != 0 {
			rez = t.RezeptID
		}
		if _, err := tx.Exec(`INSERT INTO plan_tage (plan_id, tag, rezept_id, hinweis)
			VALUES ($1,$2,$3,$4)`, id, t.Tag, rez, t.Hinweis); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return a.PlanDerWoche(p.Woche)
}

func (a *Ablage) SetzeTag(tagID, rezeptID int, hinweis string) error {
	var rez any
	if rezeptID != 0 {
		rez = rezeptID
	}
	_, err := a.db.Exec(`UPDATE plan_tage SET rezept_id = $1, hinweis = $2 WHERE id = $3`,
		rez, hinweis, tagID)
	return err
}

func (a *Ablage) HakeTagAb(tagID int, gekocht bool) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rezeptID sql.NullInt64
	var tag time.Time
	if err := tx.QueryRow(`UPDATE plan_tage SET gekocht = $1 WHERE id = $2
		RETURNING rezept_id, tag`, gekocht, tagID).Scan(&rezeptID, &tag); err != nil {
		return err
	}
	// Abgehakt heisst gekocht: das merkt sich das Rezept, damit es nicht sofort wiederkommt.
	if gekocht && rezeptID.Valid {
		if _, err := tx.Exec(`UPDATE rezepte SET zuletzt_gekocht = $1 WHERE id = $2`,
			tag, rezeptID.Int64); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Welche Rezepte in den letzten Wochen schon auf einem Plan standen.
func (a *Ablage) KuerzlichGeplant(vorWochen int) (map[int]string, error) {
	zeilen, err := a.db.Query(`SELECT t.rezept_id, MAX(t.tag) FROM plan_tage t
		WHERE t.rezept_id IS NOT NULL AND t.tag > current_date - ($1::int * 7)
		GROUP BY t.rezept_id`, vorWochen)
	if err != nil {
		return nil, err
	}
	defer zeilen.Close()
	raus := map[int]string{}
	for zeilen.Next() {
		var id int
		var tag time.Time
		if err := zeilen.Scan(&id, &tag); err != nil {
			return nil, err
		}
		raus[id] = tag.Format("2006-01-02")
	}
	return raus, zeilen.Err()
}

// ---------- Einkauf ----------

func (a *Ablage) Einkauf(planID int) ([]Posten, error) {
	zeilen, err := a.db.Query(`SELECT id, name, menge, einheit, kategorie, abgehakt, zusatz
		FROM einkauf WHERE plan_id = $1 ORDER BY kategorie, name`, planID)
	if err != nil {
		return nil, err
	}
	defer zeilen.Close()
	liste := []Posten{}
	for zeilen.Next() {
		var p Posten
		if err := zeilen.Scan(&p.ID, &p.Name, &p.Menge, &p.Einheit, &p.Kategorie, &p.Abgehakt, &p.Zusatz); err != nil {
			return nil, err
		}
		liste = append(liste, p)
	}
	return liste, zeilen.Err()
}

func (a *Ablage) ErsetzeEinkauf(planID int, posten []Posten) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM einkauf WHERE plan_id = $1 AND NOT zusatz`, planID); err != nil {
		return err
	}
	for _, p := range posten {
		if _, err := tx.Exec(`INSERT INTO einkauf (plan_id, name, menge, einheit, kategorie)
			VALUES ($1,$2,$3,$4,$5)`, planID, p.Name, p.Menge, p.Einheit, p.Kategorie); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *Ablage) FuegePosten(planID int, p Posten) (Posten, error) {
	err := a.db.QueryRow(`INSERT INTO einkauf (plan_id, name, menge, einheit, kategorie, zusatz)
		VALUES ($1,$2,$3,$4,$5,true) RETURNING id, name, menge, einheit, kategorie, abgehakt, zusatz`,
		planID, p.Name, p.Menge, p.Einheit, kategorieOder(p)).
		Scan(&p.ID, &p.Name, &p.Menge, &p.Einheit, &p.Kategorie, &p.Abgehakt, &p.Zusatz)
	return p, err
}

func kategorieOder(p Posten) string {
	if p.Kategorie != "" {
		return p.Kategorie
	}
	return kategorieRaten(p.Name)
}

func (a *Ablage) HakePostenAb(id int, abgehakt bool) error {
	_, err := a.db.Exec(`UPDATE einkauf SET abgehakt = $1 WHERE id = $2`, abgehakt, id)
	return err
}

func (a *Ablage) LoeschePosten(id int) error {
	_, err := a.db.Exec(`DELETE FROM einkauf WHERE id = $1`, id)
	return err
}

// ---------- Vorrat und Regeln ----------

func (a *Ablage) Vorrat() ([]string, error) {
	zeilen, err := a.db.Query(`SELECT name FROM vorrat ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer zeilen.Close()
	liste := []string{}
	for zeilen.Next() {
		var n string
		if err := zeilen.Scan(&n); err != nil {
			return nil, err
		}
		liste = append(liste, n)
	}
	return liste, zeilen.Err()
}

func (a *Ablage) FuegeVorrat(name string) error {
	_, err := a.db.Exec(`INSERT INTO vorrat (name) VALUES ($1) ON CONFLICT DO NOTHING`,
		strings.TrimSpace(name))
	return err
}

func (a *Ablage) LoescheVorrat(name string) error {
	_, err := a.db.Exec(`DELETE FROM vorrat WHERE name = $1`, name)
	return err
}

func (a *Ablage) Regeln() (map[string]string, error) {
	zeilen, err := a.db.Query(`SELECT schluessel, wert FROM regeln`)
	if err != nil {
		return nil, err
	}
	defer zeilen.Close()
	raus := map[string]string{}
	for zeilen.Next() {
		var k, v string
		if err := zeilen.Scan(&k, &v); err != nil {
			return nil, err
		}
		raus[k] = v
	}
	return raus, zeilen.Err()
}

func (a *Ablage) SetzeRegel(k, v string) error {
	_, err := a.db.Exec(`INSERT INTO regeln (schluessel, wert) VALUES ($1,$2)
		ON CONFLICT (schluessel) DO UPDATE SET wert = EXCLUDED.wert`, k, v)
	return err
}

func (a *Ablage) AnzahlRezepte() int {
	var n int
	_ = a.db.QueryRow(`SELECT count(*) FROM rezepte`).Scan(&n)
	return n
}

// einerVon laesst nur bekannte Werte durch; alles andere wird leer und
// bekommt in ordne() die Vorgabe.
func einerVon(wert string, erlaubt ...string) string {
	w := strings.ToLower(strings.TrimSpace(wert))
	for _, e := range erlaubt {
		if w == e {
			return e
		}
	}
	return ""
}

// ---------- Eigene Lebensmittel und Zuordnungen ----------

type EigenesLebensmittel struct {
	ID      int                `json:"id"`
	Name    string             `json:"name"`
	StueckG float64            `json:"stueck_g"`
	Werte   map[string]float64 `json:"werte"`
}

func (a *Ablage) EigeneLebensmittel() ([]EigenesLebensmittel, error) {
	zeilen, err := a.db.Query(`SELECT id, name, stueck_g, werte FROM lebensmittel ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer zeilen.Close()
	liste := []EigenesLebensmittel{}
	for zeilen.Next() {
		var e EigenesLebensmittel
		var werte []byte
		if err := zeilen.Scan(&e.ID, &e.Name, &e.StueckG, &werte); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(werte, &e.Werte); err != nil {
			return nil, err
		}
		liste = append(liste, e)
	}
	return liste, zeilen.Err()
}

func (a *Ablage) SpeichereLebensmittel(e EigenesLebensmittel) (int, error) {
	e.Name = strings.TrimSpace(e.Name)
	if e.Name == "" {
		return 0, fmt.Errorf("Name fehlt")
	}
	if e.Werte == nil {
		e.Werte = map[string]float64{}
	}
	werte, err := json.Marshal(e.Werte)
	if err != nil {
		return 0, err
	}
	if e.ID == 0 {
		err = a.db.QueryRow(`INSERT INTO lebensmittel (name, stueck_g, werte) VALUES ($1,$2,$3) RETURNING id`,
			e.Name, e.StueckG, werte).Scan(&e.ID)
		return e.ID, err
	}
	res, err := a.db.Exec(`UPDATE lebensmittel SET name=$2, stueck_g=$3, werte=$4 WHERE id=$1`,
		e.ID, e.Name, e.StueckG, werte)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, fmt.Errorf("nicht gefunden")
	}
	return e.ID, nil
}

func (a *Ablage) LoescheLebensmittel(id int) error {
	_, err := a.db.Exec(`DELETE FROM lebensmittel WHERE id = $1`, id)
	return err
}

func (a *Ablage) Zuordnungen() (map[string]Zuordnung, error) {
	zeilen, err := a.db.Query(`SELECT zutat, lebensmittel, stueck_g FROM zuordnung`)
	if err != nil {
		return nil, err
	}
	defer zeilen.Close()
	raus := map[string]Zuordnung{}
	for zeilen.Next() {
		var z Zuordnung
		if err := zeilen.Scan(&z.Zutat, &z.Lebensmittel, &z.StueckG); err != nil {
			return nil, err
		}
		raus[z.Zutat] = z
	}
	return raus, zeilen.Err()
}

func (a *Ablage) SetzeZuordnung(z Zuordnung) error {
	_, err := a.db.Exec(`INSERT INTO zuordnung (zutat, lebensmittel, stueck_g) VALUES ($1,$2,$3)
		ON CONFLICT (zutat) DO UPDATE SET lebensmittel = EXCLUDED.lebensmittel, stueck_g = EXCLUDED.stueck_g`,
		z.Zutat, z.Lebensmittel, z.StueckG)
	return err
}

func (a *Ablage) LoescheZuordnung(zutat string) error {
	_, err := a.db.Exec(`DELETE FROM zuordnung WHERE zutat = $1`, zutat)
	return err
}
