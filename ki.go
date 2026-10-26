package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Anbindung an die eigene Ollama-Instanz im Heimnetz. Nichts verlaesst das Netz.

type KI struct {
	Adresse string // z. B. http://localhost:11434
	Modell  string
	Klient  *http.Client
}

func NeueKI(adresse, modell string, zeit time.Duration) *KI {
	return &KI{
		Adresse: strings.TrimRight(adresse, "/"),
		Modell:  modell,
		Klient:  &http.Client{Timeout: zeit},
	}
}

type nachricht struct {
	Rolle  string `json:"role"`
	Inhalt string `json:"content"`
}

type chatAnfrage struct {
	Modell      string         `json:"model"`
	Nachrichten []nachricht    `json:"messages"`
	Strom       bool           `json:"stream"`
	Format      string         `json:"format,omitempty"`
	KeepAlive   string         `json:"keep_alive,omitempty"`
	Optionen    map[string]any `json:"options,omitempty"`
}

type chatAntwort struct {
	Nachricht nachricht `json:"message"`
	Fehler    string    `json:"error"`
}

// Fragt das Modell und erwartet ein JSON-Objekt zurueck.
func (k *KI) FrageJSON(ctx context.Context, anweisung, auftrag string, ziel any) error {
	rumpf, _ := json.Marshal(chatAnfrage{
		Modell: k.Modell,
		Nachrichten: []nachricht{
			{Rolle: "system", Inhalt: anweisung},
			{Rolle: "user", Inhalt: auftrag},
		},
		Strom:     false,
		Format:    "json",
		KeepAlive: "15m",
		Optionen:  map[string]any{"temperature": 0.7, "num_ctx": 8192},
	})
	anfrage, err := http.NewRequestWithContext(ctx, http.MethodPost, k.Adresse+"/api/chat", bytes.NewReader(rumpf))
	if err != nil {
		return err
	}
	anfrage.Header.Set("Content-Type", "application/json")
	antwort, err := k.Klient.Do(anfrage)
	if err != nil {
		return fmt.Errorf("Ollama nicht erreichbar: %w", err)
	}
	defer antwort.Body.Close()
	daten, err := io.ReadAll(antwort.Body)
	if err != nil {
		return err
	}
	if antwort.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama antwortet %d: %s", antwort.StatusCode, kurz(string(daten)))
	}
	var a chatAntwort
	if err := json.Unmarshal(daten, &a); err != nil {
		return fmt.Errorf("Antwort unlesbar: %w", err)
	}
	if a.Fehler != "" {
		return fmt.Errorf("Ollama: %s", a.Fehler)
	}
	text := schaeleJSON(a.Nachricht.Inhalt)
	if err := json.Unmarshal([]byte(text), ziel); err != nil {
		return fmt.Errorf("Modellantwort ist kein brauchbares JSON: %w (%s)", err, kurz(text))
	}
	return nil
}

// Manche Modelle legen das JSON in einen Codeblock oder schreiben davor.
func schaeleJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if j := strings.Index(rest, "\n"); j >= 0 {
			rest = rest[j+1:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			rest = rest[:j]
		}
		s = strings.TrimSpace(rest)
	}
	anfang := strings.IndexAny(s, "{[")
	if anfang > 0 {
		s = s[anfang:]
	}
	ende := strings.LastIndexAny(s, "}]")
	if ende >= 0 && ende < len(s)-1 {
		s = s[:ende+1]
	}
	return s
}

func kurz(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func (k *KI) Erreichbar(ctx context.Context) (bool, []string) {
	anfrage, err := http.NewRequestWithContext(ctx, http.MethodGet, k.Adresse+"/api/tags", nil)
	if err != nil {
		return false, nil
	}
	antwort, err := k.Klient.Do(anfrage)
	if err != nil {
		return false, nil
	}
	defer antwort.Body.Close()
	var d struct {
		Modelle []struct{ Name string } `json:"models"`
	}
	if json.NewDecoder(antwort.Body).Decode(&d) != nil {
		return false, nil
	}
	namen := []string{}
	for _, m := range d.Modelle {
		namen = append(namen, m.Name)
	}
	return true, namen
}
