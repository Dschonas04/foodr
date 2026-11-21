package main

import (
	"strings"
	"testing"
)

func TestIcsTextOhneZeilenumbruch(t *testing.T) {
	for _, ein := range []string{"a\rEND:VEVENT", "a\nBEGIN:VEVENT", "a\r\nX:1", "a\x0bb\x00c"} {
		aus := icsText(ein)
		if strings.ContainsAny(aus, "\r\n\x0b\x00") {
			t.Fatalf("%q ergibt %q", ein, aus)
		}
	}
	if got := icsText("Salz, Pfeffer; Öl\nfertig"); got != `Salz\, Pfeffer\; Öl\nfertig` {
		t.Fatalf("Maskierung falsch: %q", got)
	}
}
