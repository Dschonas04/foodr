// Anmeldung über einen OpenID-Connect-Anbieter (Keycloak & Co.).
//
// Der Baustein ist in allen eigenen Apps derselbe und kommt ohne fremde
// Bibliotheken aus. Eingerichtet wird er in den Einstellungen der App, die
// Werte landen in einer JSON-Datei neben den übrigen Daten.
//
// Ablauf: Code-Flow mit PKCE. Das id_token kommt direkt vom Token-Endpunkt des
// Anbieters, über TLS und mit dem Client-Geheimnis abgeholt; nach OIDC Core
// 3.1.3.7 darf die TLS-Prüfung dann die Signaturprüfung ersetzen. Aussteller,
// Empfänger, Ablauf und Nonce werden trotzdem geprüft.
//
// Notausgang: steht <APP>_SSO_AUS=1 in der Umgebung, ist SSO abgeschaltet, auch
// wenn es in den Einstellungen an ist. Für den Fall, dass man sich mit einer
// falschen Einstellung ausgesperrt hat.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SSOKonfig ist das, was in den Einstellungen steht.
type SSOKonfig struct {
	Aktiv      bool   `json:"aktiv"`
	Aussteller string `json:"aussteller"`
	ClientID   string `json:"clientId"`
	Geheimnis  string `json:"geheimnis,omitempty"`
	// Adresse, unter der die App erreichbar ist. Leer: aus der Anfrage.
	Adresse string `json:"adresse"`
	// Kommagetrennte E-Mail-Adressen (nur bestätigte zählen), Nutzernamen
	// oder Kennungen (sub). Leer: jeder, den der Anbieter anmeldet.
	Erlaubt string `json:"erlaubt"`
}

// SSONutzer ist die angemeldete Person.
type SSONutzer struct {
	Kennung string `json:"sub"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Nutzer  string `json:"nutzer"`
	// Ob der Anbieter die Adresse als bestätigt meldet.
	EmailBestaetigt bool `json:"emailBestaetigt"`
}

type ssoDatei struct {
	SSOKonfig
	Schluessel string `json:"schluessel"`
}

// SSO hält Einstellung und Sitzungsschlüssel einer App.
type SSO struct {
	App   string // Anzeigename, z. B. "Foodr"
	datei string
	// Schalter aus der Umgebung, siehe oben.
	notaus bool
	// Sperre: true = ohne Anmeldung kommt bei aktivem SSO niemand herein.
	// Apps mit eigenen Konten setzen false und melden über Anmelden an.
	Sperre bool
	// Anmelden macht aus dem Nutzer eine Sitzung. Leer: eigenes Keks.
	Anmelden func(w http.ResponseWriter, r *http.Request, n SSONutzer) error
	// Nach der Abmeldung hierhin, wenn kein Anbieter-Logout möglich ist.
	Startseite string
	// Frei sind Pfade, die auch ohne Anmeldung gehen (Präfixe).
	Frei []string
	// Durchlass lässt Anfragen ohne Sitzung zu, etwa Skripte mit eigenem
	// Schlüssel. nil = keine.
	Durchlass func(r *http.Request) bool

	mu         sync.RWMutex
	k          SSOKonfig
	schluessel []byte
	client     *http.Client
}

const (
	ssoKeks     = "sso_sitzung"
	ssoWegKeks  = "sso_weg"
	ssoDauer    = 14 * 24 * time.Hour
	ssoWegDauer = 10 * time.Minute
)

// NeuesSSO lädt die Einstellung aus datei oder legt sie an.
func NeuesSSO(app, datei, notausVariable string) *SSO {
	s := &SSO{App: app, datei: datei, Sperre: true, Startseite: "/",
		notaus: os.Getenv(notausVariable) == "1",
		client: &http.Client{Timeout: 15 * time.Second}}
	var d ssoDatei
	if roh, err := os.ReadFile(datei); err == nil {
		if err := json.Unmarshal(roh, &d); err != nil {
			log.Printf("sso: %s nicht lesbar: %v", datei, err)
		}
	}
	s.k = d.SSOKonfig
	if b, err := base64.RawURLEncoding.DecodeString(d.Schluessel); err == nil && len(b) >= 32 {
		s.schluessel = b
	} else {
		s.schluessel = ssoZufallsBytes(32)
		if err := s.sichern(); err != nil {
			log.Printf("sso: Schlüssel nicht gespeichert: %v", err)
		}
	}
	if s.notaus {
		log.Printf("sso: per %s abgeschaltet", notausVariable)
	}
	return s
}

func (s *SSO) sichern() error {
	d := ssoDatei{SSOKonfig: s.k, Schluessel: base64.RawURLEncoding.EncodeToString(s.schluessel)}
	roh, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.datei), 0o700); err != nil {
		return err
	}
	tmp := s.datei + ".neu"
	if err := os.WriteFile(tmp, roh, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.datei)
}

// An sagt, ob SSO gerade gilt.
func (s *SSO) An() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.notaus && s.k.Aktiv && s.k.Aussteller != "" && s.k.ClientID != ""
}

func (s *SSO) konfig() SSOKonfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.k
}

// Wege hängt die SSO-Adressen an. einstellen schützt das Ändern der
// Einstellung zusätzlich (z. B. mit dem Kennwort der App); nil = nur SSO.
func (s *SSO) Wege(m *http.ServeMux, einstellen func(http.HandlerFunc) http.HandlerFunc) {
	if s == nil {
		return
	}
	if einstellen == nil {
		einstellen = func(h http.HandlerFunc) http.HandlerFunc { return h }
	}
	m.HandleFunc("GET /sso/anmelden", s.start)
	m.HandleFunc("GET /sso/zurueck", s.zurueck)
	m.HandleFunc("GET /sso/abmelden", s.abmelden)
	m.HandleFunc("POST /sso/abmelden", s.abmelden)
	m.HandleFunc("GET /api/sso/status", s.status)
	// Für einen vorgeschalteten nginx (auth_request): 204 = herein, 401 = anmelden.
	m.HandleFunc("GET /api/sso/pruefen", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.Wer(r); ok || !s.Sperre || !s.An() || (s.Durchlass != nil && s.Durchlass(r)) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	m.HandleFunc("GET /api/sso/einstellungen", einstellen(s.einstellungenLesen))
	m.HandleFunc("PUT /api/sso/einstellungen", einstellen(s.einstellungenSchreiben))
	m.HandleFunc("POST /api/sso/testen", einstellen(s.testen))
}

// Schutz lässt bei aktivem SSO nur Angemeldete durch.
func (s *SSO) Schutz(n http.Handler) http.Handler {
	if s == nil {
		return n
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Sperre || !s.An() || s.frei(r.URL.Path) {
			n.ServeHTTP(w, r)
			return
		}
		if _, ok := s.Wer(r); ok || (s.Durchlass != nil && s.Durchlass(r)) {
			n.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || r.Method != http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"fehler":"Bitte anmelden","error":"Bitte anmelden","sso":"/sso/anmelden"}`)
			return
		}
		http.Redirect(w, r, "/sso/anmelden?ziel="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
	})
}

func (s *SSO) frei(pfad string) bool {
	if strings.HasPrefix(pfad, "/sso/") || pfad == "/api/sso/status" || pfad == "/api/sso/pruefen" ||
		pfad == "/favicon.svg" || pfad == "/favicon.ico" {
		return true
	}
	for _, p := range s.Frei {
		if strings.HasPrefix(pfad, p) {
			return true
		}
	}
	return false
}

// Wer liest die Sitzung aus dem Keks.
func (s *SSO) Wer(r *http.Request) (SSONutzer, bool) {
	if s == nil {
		return SSONutzer{}, false
	}
	var n ssoSitzung
	if !s.lesen(r, ssoKeks, &n) || time.Now().Unix() > n.Bis {
		return SSONutzer{}, false
	}
	// Wechselt der Anbieter, der Client oder die Freigabeliste, gelten alte
	// Sitzungen nicht mehr.
	k := s.konfig()
	if n.Aussteller != k.Aussteller || n.ClientID != k.ClientID || !ssoErlaubt(k.Erlaubt, n.SSONutzer) {
		return SSONutzer{}, false
	}
	return n.SSONutzer, true
}

type ssoSitzung struct {
	SSONutzer
	Aussteller string `json:"iss"`
	ClientID   string `json:"aud"`
	Bis        int64  `json:"bis"`
}

// ---------- Kekse ----------

func (s *SSO) unterschrift(daten string) string {
	s.mu.RLock()
	m := hmac.New(sha256.New, s.schluessel)
	s.mu.RUnlock()
	m.Write([]byte(daten))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *SSO) setzen(w http.ResponseWriter, r *http.Request, name string, wert any, dauer time.Duration) {
	roh, _ := json.Marshal(wert)
	daten := base64.RawURLEncoding.EncodeToString(roh)
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: daten + "." + s.unterschrift(name+"|"+daten), Path: "/",
		MaxAge: int(dauer.Seconds()), HttpOnly: true, Secure: ssoSicher(r), SameSite: http.SameSiteLaxMode,
	})
}

func (s *SSO) lesen(r *http.Request, name string, ziel any) bool {
	c, err := r.Cookie(name)
	if err != nil {
		return false
	}
	daten, sig, ok := strings.Cut(c.Value, ".")
	if !ok || subtle.ConstantTimeCompare([]byte(sig), []byte(s.unterschrift(name+"|"+daten))) != 1 {
		return false
	}
	roh, err := base64.RawURLEncoding.DecodeString(daten)
	return err == nil && json.Unmarshal(roh, ziel) == nil
}

func ssoLoeschen(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: ssoSicher(r), SameSite: http.SameSiteLaxMode})
}

func ssoSicher(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// ---------- Anbieter ----------

type ssoAnbieter struct {
	Issuer   string `json:"issuer"`
	Auth     string `json:"authorization_endpoint"`
	Token    string `json:"token_endpoint"`
	Abmelden string `json:"end_session_endpoint"`
}

func (s *SSO) anbieter(ctx context.Context, aussteller string) (ssoAnbieter, error) {
	var a ssoAnbieter
	aussteller = strings.TrimSuffix(strings.TrimSpace(aussteller), "/")
	if aussteller == "" {
		return a, errors.New("kein Aussteller eingetragen")
	}
	u, err := url.Parse(aussteller)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && ssoLokal(u.Hostname()))) {
		return a, errors.New("Der Aussteller muss eine https-Adresse sein.")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, aussteller+"/.well-known/openid-configuration", nil)
	res, err := s.client.Do(req)
	if err != nil {
		log.Printf("sso: %s nicht erreichbar: %v", aussteller, err)
		return a, errors.New("Aussteller nicht erreichbar")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return a, fmt.Errorf("Aussteller antwortet mit %s", res.Status)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&a); err != nil {
		return a, errors.New("Antwort des Ausstellers unlesbar")
	}
	if strings.TrimSuffix(a.Issuer, "/") != aussteller {
		return a, errors.New("Der Aussteller meldet sich unter einer anderen Adresse")
	}
	if a.Auth == "" || a.Token == "" {
		return a, errors.New("Aussteller nennt keine Endpunkte")
	}
	return a, nil
}

func (s *SSO) basis(r *http.Request) string {
	if a := strings.TrimSuffix(strings.TrimSpace(s.konfig().Adresse), "/"); a != "" {
		return a
	}
	schema := "http"
	if ssoSicher(r) {
		schema = "https"
	}
	return schema + "://" + r.Host
}

func (s *SSO) rueckweg(r *http.Request) string { return s.basis(r) + "/sso/zurueck" }

// ---------- Anmelden ----------

type ssoWeg struct {
	Zustand string `json:"z"`
	Nonce   string `json:"n"`
	Pruefer string `json:"p"`
	Ziel    string `json:"ziel"`
}

func (s *SSO) start(w http.ResponseWriter, r *http.Request) {
	if !s.An() {
		http.Redirect(w, r, s.Startseite, http.StatusFound)
		return
	}
	k := s.konfig()
	a, err := s.anbieter(r.Context(), k.Aussteller)
	if err != nil {
		s.fehlerSeite(w, err.Error())
		return
	}
	weg := ssoWeg{Zustand: ssoZufall(), Nonce: ssoZufall(), Pruefer: ssoZufall() + ssoZufall(), Ziel: ssoZiel(r.URL.Query().Get("ziel"))}
	s.setzen(w, r, ssoWegKeks, weg, ssoWegDauer)
	h := sha256.Sum256([]byte(weg.Pruefer))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {k.ClientID},
		"redirect_uri":          {s.rueckweg(r)},
		"scope":                 {"openid email profile"},
		"state":                 {weg.Zustand},
		"nonce":                 {weg.Nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(h[:])},
		"code_challenge_method": {"S256"},
	}
	trenner := "?"
	if strings.Contains(a.Auth, "?") {
		trenner = "&"
	}
	http.Redirect(w, r, a.Auth+trenner+q.Encode(), http.StatusFound)
}

func (s *SSO) zurueck(w http.ResponseWriter, r *http.Request) {
	var weg ssoWeg
	if !s.lesen(r, ssoWegKeks, &weg) {
		s.fehlerSeite(w, "Die Anmeldung ist abgelaufen. Bitte noch einmal versuchen.")
		return
	}
	ssoLoeschen(w, r, ssoWegKeks)
	q := r.URL.Query()
	if weg.Zustand == "" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(weg.Zustand)) != 1 {
		s.fehlerSeite(w, "Die Antwort gehört nicht zu dieser Anmeldung.")
		return
	}
	if f := q.Get("error"); f != "" {
		s.fehlerSeite(w, "Der Anbieter hat abgelehnt: "+f)
		return
	}
	if !s.An() {
		s.fehlerSeite(w, "SSO ist nicht eingeschaltet.")
		return
	}
	k := s.konfig()
	a, err := s.anbieter(r.Context(), k.Aussteller)
	if err != nil {
		s.fehlerSeite(w, err.Error())
		return
	}
	nutzer, err := s.einloesen(r, k, a, q.Get("code"), weg)
	if err != nil {
		log.Printf("sso: Anmeldung abgelehnt: %v", err)
		s.fehlerSeite(w, err.Error())
		return
	}
	if !ssoErlaubt(k.Erlaubt, nutzer) {
		log.Printf("sso: %s (%s) ist nicht freigegeben", nutzer.Nutzer, nutzer.Email)
		s.fehlerSeite(w, "Dein Konto ist für "+s.App+" nicht freigegeben.")
		return
	}
	if s.Anmelden != nil {
		if err := s.Anmelden(w, r, nutzer); err != nil {
			s.fehlerSeite(w, err.Error())
			return
		}
	} else {
		s.setzen(w, r, ssoKeks, ssoSitzung{SSONutzer: nutzer, Aussteller: k.Aussteller, ClientID: k.ClientID,
			Bis: time.Now().Add(ssoDauer).Unix()}, ssoDauer)
	}
	ziel := weg.Ziel
	if ziel == "" {
		ziel = s.Startseite
	}
	http.Redirect(w, r, ziel, http.StatusFound)
}

func (s *SSO) einloesen(r *http.Request, k SSOKonfig, a ssoAnbieter, code string, weg ssoWeg) (SSONutzer, error) {
	var n SSONutzer
	if code == "" {
		return n, errors.New("Der Anbieter hat keinen Code geschickt.")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {s.rueckweg(r)},
		"client_id":     {k.ClientID},
		"code_verifier": {weg.Pruefer},
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, a.Token, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if k.Geheimnis != "" {
		req.SetBasicAuth(url.QueryEscape(k.ClientID), url.QueryEscape(k.Geheimnis))
	}
	res, err := s.client.Do(req)
	if err != nil {
		return n, fmt.Errorf("Token-Endpunkt nicht erreichbar: %v", err)
	}
	defer res.Body.Close()
	var antwort struct {
		IDToken string `json:"id_token"`
		Fehler  string `json:"error"`
		Text    string `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&antwort)
	if res.StatusCode != http.StatusOK {
		return n, fmt.Errorf("Der Code wurde nicht angenommen (%s %s).", antwort.Fehler, antwort.Text)
	}
	teile := strings.Split(antwort.IDToken, ".")
	if len(teile) != 3 {
		return n, errors.New("Der Anbieter hat kein id_token geschickt.")
	}
	roh, err := base64.RawURLEncoding.DecodeString(teile[1])
	if err != nil {
		return n, errors.New("Das id_token ist unlesbar.")
	}
	var c struct {
		Iss     string          `json:"iss"`
		Aud     json.RawMessage `json:"aud"`
		Exp     int64           `json:"exp"`
		Nonce   string          `json:"nonce"`
		Sub     string          `json:"sub"`
		Name    string          `json:"name"`
		Email   string          `json:"email"`
		Bestaet bool            `json:"email_verified"`
		Nutzer  string          `json:"preferred_username"`
	}
	if err := json.Unmarshal(roh, &c); err != nil {
		return n, errors.New("Das id_token ist unlesbar.")
	}
	if strings.TrimSuffix(c.Iss, "/") != strings.TrimSuffix(a.Issuer, "/") {
		return n, errors.New("Das id_token stammt von einem anderen Aussteller.")
	}
	if !ssoEmpfaenger(c.Aud, k.ClientID) {
		return n, errors.New("Das id_token ist nicht für diese App.")
	}
	if time.Now().Unix() > c.Exp+60 {
		return n, errors.New("Das id_token ist abgelaufen.")
	}
	if subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(weg.Nonce)) != 1 {
		return n, errors.New("Die Kennung der Anmeldung passt nicht.")
	}
	if c.Sub == "" {
		return n, errors.New("Das id_token nennt niemanden.")
	}
	n = SSONutzer{Kennung: c.Sub, Name: c.Name, Email: strings.ToLower(c.Email), Nutzer: c.Nutzer, EmailBestaetigt: c.Bestaet}
	if n.Name == "" {
		n.Name = n.Nutzer
	}
	return n, nil
}

func ssoEmpfaenger(aud json.RawMessage, client string) bool {
	var eins string
	if json.Unmarshal(aud, &eins) == nil {
		return eins == client
	}
	var viele []string
	if json.Unmarshal(aud, &viele) == nil {
		for _, a := range viele {
			if a == client {
				return true
			}
		}
	}
	return false
}

func ssoErlaubt(liste string, n SSONutzer) bool {
	liste = strings.TrimSpace(liste)
	if liste == "" {
		return true
	}
	for _, e := range strings.FieldsFunc(liste, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		// Ein Eintrag mit @ ist eine Adresse und zählt nur, wenn der Anbieter
		// sie als bestätigt meldet: sonst könnte sich jemand eine fremde
		// Adresse ins Profil oder als Nutzernamen eintragen.
		if strings.Contains(e, "@") {
			if n.EmailBestaetigt && e == n.Email {
				return true
			}
			continue
		}
		if e == strings.ToLower(n.Nutzer) || e == strings.ToLower(n.Kennung) {
			return true
		}
	}
	return false
}

func (s *SSO) abmelden(w http.ResponseWriter, r *http.Request) {
	ssoLoeschen(w, r, ssoKeks)
	if !s.An() {
		http.Redirect(w, r, s.Startseite, http.StatusFound)
		return
	}
	k := s.konfig()
	a, err := s.anbieter(r.Context(), k.Aussteller)
	if err != nil || a.Abmelden == "" {
		http.Redirect(w, r, s.Startseite, http.StatusFound)
		return
	}
	q := url.Values{"client_id": {k.ClientID}, "post_logout_redirect_uri": {s.basis(r) + "/"}}
	http.Redirect(w, r, a.Abmelden+"?"+q.Encode(), http.StatusFound)
}

// ---------- Einstellungen ----------

func (s *SSO) status(w http.ResponseWriter, r *http.Request) {
	n, ok := s.Wer(r)
	antwort := map[string]any{"aktiv": s.An(), "notaus": s.notaus, "angemeldet": ok, "anmelden": "/sso/anmelden", "abmelden": "/sso/abmelden"}
	if ok {
		antwort["nutzer"] = n
	}
	ssoJSON(w, http.StatusOK, antwort)
}

func (s *SSO) einstellungenLesen(w http.ResponseWriter, r *http.Request) {
	k := s.konfig()
	geheim := k.Geheimnis != ""
	k.Geheimnis = ""
	ssoJSON(w, http.StatusOK, map[string]any{
		"einstellung": k,
		"geheimnisDa": geheim,
		"rueckweg":    s.rueckweg(r),
		"abmeldeweg":  s.basis(r) + "/",
		"notaus":      s.notaus,
	})
}

func (s *SSO) einstellungenSchreiben(w http.ResponseWriter, r *http.Request) {
	var neu struct {
		SSOKonfig
		GeheimnisBehalten bool `json:"geheimnisBehalten"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&neu); err != nil {
		ssoJSON(w, http.StatusBadRequest, map[string]string{"fehler": "Ungültige Angaben"})
		return
	}
	neu.Aussteller = strings.TrimSuffix(strings.TrimSpace(neu.Aussteller), "/")
	neu.ClientID = strings.TrimSpace(neu.ClientID)
	neu.Adresse = strings.TrimSuffix(strings.TrimSpace(neu.Adresse), "/")
	alt := s.konfig()
	if neu.GeheimnisBehalten || neu.Geheimnis == "" {
		// Das alte Geheimnis bleibt nur beim selben Anbieter und Client:
		// sonst ließe es sich an einen fremden Token-Endpunkt umlenken.
		if neu.Aussteller != alt.Aussteller || neu.ClientID != alt.ClientID {
			neu.Geheimnis = ""
		} else {
			neu.Geheimnis = alt.Geheimnis
		}
	}
	if neu.Adresse != "" {
		if u, err := url.Parse(neu.Adresse); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			ssoJSON(w, http.StatusBadRequest, map[string]string{"fehler": "Die Adresse der App muss mit http:// oder https:// beginnen."})
			return
		}
	}
	if neu.Aktiv {
		if neu.ClientID == "" {
			ssoJSON(w, http.StatusBadRequest, map[string]string{"fehler": "Ohne Client-ID lässt sich SSO nicht einschalten."})
			return
		}
		if _, err := s.anbieter(r.Context(), neu.Aussteller); err != nil {
			ssoJSON(w, http.StatusBadRequest, map[string]string{"fehler": err.Error()})
			return
		}
	}
	s.mu.Lock()
	s.k = neu.SSOKonfig
	err := s.sichern()
	s.mu.Unlock()
	if err != nil {
		ssoJSON(w, http.StatusInternalServerError, map[string]string{"fehler": "Nicht gespeichert: " + err.Error()})
		return
	}
	log.Printf("sso: Einstellung geändert (aktiv=%v, aussteller=%s, client=%s)", neu.Aktiv, neu.Aussteller, neu.ClientID)
	s.einstellungenLesen(w, r)
}

func (s *SSO) testen(w http.ResponseWriter, r *http.Request) {
	var t struct {
		Aussteller string `json:"aussteller"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&t)
	if strings.TrimSpace(t.Aussteller) == "" {
		t.Aussteller = s.konfig().Aussteller
	}
	a, err := s.anbieter(r.Context(), t.Aussteller)
	if err != nil {
		ssoJSON(w, http.StatusOK, map[string]any{"ok": false, "meldung": err.Error()})
		return
	}
	_ = a
	ssoJSON(w, http.StatusOK, map[string]any{"ok": true, "meldung": "Aussteller erreichbar."})
}

// ---------- Kleinkram ----------

func ssoJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *SSO) fehlerSeite(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprintf(w, `<!doctype html><html lang="de"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>%[1]s – Anmeldung</title>
<style>body{font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif;background:#f7f7f6;color:#37352f;display:grid;place-items:center;min-height:100vh;margin:0}
main{background:#fff;border:1px solid #e2e2df;border-radius:10px;padding:28px 32px;max-width:440px}
h1{font-size:20px;margin:0 0 8px}a{color:#1b6ec2}
@media (prefers-color-scheme:dark){body{background:#1f1f1e;color:#e6e5e2}main{background:#2a2a28;border-color:#3a3a37}a{color:#4a9ae8}}</style>
<main><h1>Anmeldung bei %[1]s nicht möglich</h1><p>%[2]s</p><p><a href="/sso/anmelden">Noch einmal versuchen</a></p></main></html>`,
		ssoHTML(s.App), ssoHTML(text))
}

func ssoHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// sicheresZiel lässt nur Pfade dieser App zu, keine fremden Adressen.
func ssoZiel(z string) string {
	if !strings.HasPrefix(z, "/") || strings.HasPrefix(z, "//") || strings.HasPrefix(z, "/sso/") ||
		strings.ContainsAny(z, "\\\r\n\t") || len(z) > 2000 {
		return ""
	}
	for _, c := range z {
		if c < 0x20 || c == 0x7f {
			return ""
		}
	}
	u, err := url.Parse(z)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return ""
	}
	return z
}

func ssoLokal(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func ssoZufallsBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func ssoZufall() string { return base64.RawURLEncoding.EncodeToString(ssoZufallsBytes(24)) }
