package main

// Feste Einteilung der Rezepte: nach Art des Gerichts und nach Kueche.
// Die Oberflaeche zeigt den Pool danach gegliedert, das Modell waehlt beim
// Schreiben genau einen Wert aus jeder Liste.

import "strings"

var Gruppen = []string{
	"Suppen", "Eintöpfe", "Nudeln", "Aufläufe und Gratins", "Pfannengerichte", "Braten und Schmorgerichte",
	"Kurzgebratenes", "Currys", "Wok und Asianudeln", "Reis und Getreide", "Kartoffelgerichte", "Gemüsegerichte",
	"Fisch und Meeresfrüchte", "Salate und Bowls", "Pizza, Quiche und Flammkuchen", "Brot und Snacks",
	"Frühstück", "Süßes",
}

var Kuechen = []string{
	"Deutsch", "Österreich und Schweiz", "Italienisch", "Französisch", "Spanien und Portugal",
	"Griechenland und Türkei", "Orient", "Afrika", "Lateinamerika", "USA und Großbritannien",
	"Japan", "Korea", "China", "Thailand und Vietnam", "Indien", "Osteuropa", "Skandinavien", "International",
}

func gruppenListe() string { return strings.Join(Gruppen, "; ") }
func kuechenListe() string { return strings.Join(Kuechen, "; ") }
