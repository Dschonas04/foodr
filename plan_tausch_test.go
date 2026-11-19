package main

import "testing"

// Mehrmals tauschen darf nicht zwischen zwei Gerichten pendeln.
func TestTauschWahlOhnePendeln(t *testing.T) {
	var rezepte []Rezept
	rang := map[int]float64{}
	for i := 1; i <= 30; i++ {
		rezepte = append(rezepte, Rezept{ID: i, DauerMin: 30})
		rang[i] = float64(100 - i)
	}
	gesperrt := map[int]bool{}
	for n := 0; n < 10; n++ {
		r := tauschWahl(rezepte, rang, gesperrt, true)
		if r == nil {
			t.Fatal("nichts gewählt")
		}
		if gesperrt[r.ID] {
			t.Fatalf("Rezept %d kam doppelt", r.ID)
		}
		gesperrt[r.ID] = true
	}
}
