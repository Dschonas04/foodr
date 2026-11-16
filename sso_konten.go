// Eigene Konten mit Benutzername und Passwort, neben SSO.
//
// Gehört zum SSO-Baustein (sso.go) und ist wie er in allen eigenen Apps
// derselbe, nur Standardbibliothek. Die Konten stehen in derselben JSON-Datei
// wie die SSO-Einstellung, das Passwort als PBKDF2-HMAC-SHA256 mit eigenem
// Salz je Konto.
//
// Verwaltet werden sie unter /sso/konten, einer schlichten Seite im Stil der
// Anmeldeseite. Solange es weder Konto noch SSO gibt, ist die App offen wie
// bisher; das erste Konto schaltet die Anmeldung ein und meldet gleich damit an.
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ssoRunden        = 600000 // OWASP-Empfehlung für PBKDF2-HMAC-SHA256
	ssoFehlFenster   = 15 * time.Minute
	ssoFehlJeAdresse = 5
	ssoFehlJeName    = 20
	ssoMinPasswort   = 8
)

var ssoNamensMuster = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

// ssoAttrappe wird geprüft, wenn es den Namen nicht gibt: Die Antwort dauert
// dann genauso lang, und an der Zeit lässt sich nicht ablesen, welche Namen es gibt.
var ssoAttrappe = ssoPasswortHash("attrappe-ohne-konto")

// ---------- Passwort ----------

func ssoPBKDF2(passwort, salz []byte, runden int) []byte {
	prf := hmac.New(sha256.New, passwort)
	prf.Write(salz)
	prf.Write([]byte{0, 0, 0, 1})
	u := prf.Sum(nil)
	t := append([]byte(nil), u...)
	for i := 1; i < runden; i++ {
		prf.Reset()
		prf.Write(u)
		u = prf.Sum(u[:0])
		for j := range t {
			t[j] ^= u[j]
		}
	}
	return t
}

func ssoPasswortHash(passwort string) string {
	salz := ssoZufallsBytes(16)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", ssoRunden,
		base64.RawStdEncoding.EncodeToString(salz),
		base64.RawStdEncoding.EncodeToString(ssoPBKDF2([]byte(passwort), salz, ssoRunden)))
}

func ssoPasswortPruefen(hash, passwort string) bool {
	teile := strings.Split(hash, "$")
	if len(teile) != 4 || teile[0] != "pbkdf2-sha256" {
		return false
	}
	runden, err := strconv.Atoi(teile[1])
	if err != nil || runden < 1 || runden > 10_000_000 {
		return false
	}
	salz, err1 := base64.RawStdEncoding.DecodeString(teile[2])
	soll, err2 := base64.RawStdEncoding.DecodeString(teile[3])
	if err1 != nil || err2 != nil || len(soll) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(ssoPBKDF2([]byte(passwort), salz, runden), soll) == 1
}

// ssoStand ist ein Fingerabdruck des Hashes für die Sitzung: Ein neues
// Passwort ergibt einen neuen Stand, alte Sitzungen gelten dann nicht mehr.
func ssoStand(hash string) string {
	h := sha256.Sum256([]byte(hash))
	return base64.RawURLEncoding.EncodeToString(h[:12])
}

func (s *SSO) konto(name string) (ssoKonto, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.konten {
		if k.Name == name {
			return k, true
		}
	}
	return ssoKonto{}, false
}

func (s *SSO) kontoStandPasst(name, stand string) bool {
	if !s.hatKonten() {
		return false
	}
	k, ok := s.konto(name)
	return ok && subtle.ConstantTimeCompare([]byte(ssoStand(k.Hash)), []byte(stand)) == 1
}

// ---------- Bremse ----------

// ssoAdresse ist die Adresse des Browsers für die Bremse. Die Apps stehen
// hinter Caddy (und Costr zusätzlich hinter nginx im Container), direkt sieht
// das Backend also nur 127.0.0.1 oder ein Docker-Netz. Dann zählt der
// rechteste Eintrag in X-Forwarded-For, der kein solcher Zwischenhalt ist: Ihn
// hat der erste eigene Proxy angehängt, einen vom Browser mitgeschickten
// Eintrag weiter links kann der nicht vorschieben.
func ssoAdresse(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		h = r.RemoteAddr
	}
	if !ssoZwischenhalt(h) {
		return h
	}
	kette := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(kette) - 1; i >= 0; i-- {
		a := strings.TrimSpace(kette[i])
		if net.ParseIP(a) != nil && !ssoZwischenhalt(a) {
			return a
		}
	}
	return h
}

// ssoZwischenhalt: Loopback und Docker-Netze (172.16.0.0/12), also die
// eigenen Proxys auf dem Rechner.
func ssoZwischenhalt(adresse string) bool {
	ip := net.ParseIP(adresse)
	if ip == nil {
		return false
	}
	_, docker, _ := net.ParseCIDR("172.16.0.0/12")
	return ip.IsLoopback() || docker.Contains(ip)
}

func (s *SSO) gebremst(name, adresse string) bool {
	s.fehlMu.Lock()
	defer s.fehlMu.Unlock()
	return s.zaehlen("n|"+name) >= ssoFehlJeName || s.zaehlen("a|"+name+"|"+adresse) >= ssoFehlJeAdresse
}

// zaehlen räumt alte Einträge weg und zählt die übrigen. Aufruf unter fehlMu.
func (s *SSO) zaehlen(schluessel string) int {
	grenze := time.Now().Add(-ssoFehlFenster)
	liste := s.fehl[schluessel]
	frisch := liste[:0]
	for _, t := range liste {
		if t.After(grenze) {
			frisch = append(frisch, t)
		}
	}
	if len(frisch) == 0 {
		delete(s.fehl, schluessel)
	} else {
		s.fehl[schluessel] = frisch
	}
	return len(frisch)
}

func (s *SSO) fehlversuch(name, adresse string) {
	s.fehlMu.Lock()
	defer s.fehlMu.Unlock()
	if s.fehl == nil {
		s.fehl = map[string][]time.Time{}
	}
	jetzt := time.Now()
	s.fehl["n|"+name] = append(s.fehl["n|"+name], jetzt)
	s.fehl["a|"+name+"|"+adresse] = append(s.fehl["a|"+name+"|"+adresse], jetzt)
}

func (s *SSO) erfolg(name, adresse string) {
	s.fehlMu.Lock()
	defer s.fehlMu.Unlock()
	delete(s.fehl, "a|"+name+"|"+adresse)
}

// gleicheHerkunft: Formulare dieser Seiten nimmt die App nur von sich selbst an.
func ssoGleicheHerkunft(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// ---------- Anmelden ----------

// passwortAnmelden: POST /sso/passwort aus der Maske der Anmeldeseite.
func (s *SSO) passwortAnmelden(w http.ResponseWriter, r *http.Request) {
	if !ssoGleicheHerkunft(r) {
		s.fehlerSeite(w, "Die Anfrage kam nicht von dieser Seite.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
	if err := r.ParseForm(); err != nil {
		s.fehlerSeite(w, "Ungültige Angaben.")
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PostForm.Get("name")))
	passwort := r.PostForm.Get("passwort")
	ziel := ssoZiel(r.PostForm.Get("ziel"))
	if !s.hatKonten() {
		http.Redirect(w, r, "/sso/seite", http.StatusFound)
		return
	}
	adresse := ssoAdresse(r)
	abgelehnt := func(code int, text string) {
		i := s.anmeldeInhalt(ziel)
		i.Code, i.Meldung, i.Name = code, text, name
		s.seite(w, i)
	}
	if s.gebremst(name, adresse) {
		log.Printf("sso: Anmeldung als %q von %s gebremst", name, adresse)
		abgelehnt(http.StatusTooManyRequests, "Zu viele Fehlversuche. Bitte in einer Viertelstunde noch einmal.")
		return
	}
	k, da := s.konto(name)
	hash := ssoAttrappe
	if da {
		hash = k.Hash
	}
	if !ssoPasswortPruefen(hash, passwort) || !da {
		s.fehlversuch(name, adresse)
		log.Printf("sso: Passwort-Anmeldung als %q von %s abgelehnt", name, adresse)
		abgelehnt(http.StatusUnauthorized, "Benutzername oder Passwort stimmt nicht.")
		return
	}
	s.erfolg(name, adresse)
	s.lokalAnmelden(w, r, k)
	if ziel == "" {
		ziel = s.Startseite
	}
	http.Redirect(w, r, ziel, http.StatusFound)
}

func (s *SSO) lokalAnmelden(w http.ResponseWriter, r *http.Request, k ssoKonto) {
	s.setzen(w, r, ssoKeks, ssoSitzung{
		SSONutzer: SSONutzer{Kennung: "konto:" + k.Name, Name: k.Name, Nutzer: k.Name},
		Lokal:     true, Stand: ssoStand(k.Hash), Bis: time.Now().Add(ssoDauer).Unix(),
	}, ssoDauer)
}

// ---------- Verwaltung ----------

// kontenZugang: Ist die App gesperrt, verwaltet nur, wer angemeldet ist.
func (s *SSO) kontenZugang(w http.ResponseWriter, r *http.Request) bool {
	if !s.Geschuetzt() {
		return true
	}
	if _, ok := s.Wer(r); ok {
		return true
	}
	if r.Method == http.MethodGet {
		http.Redirect(w, r, "/sso/seite?ziel="+url.QueryEscape("/sso/konten"), http.StatusFound)
	} else {
		s.fehlerSeite(w, "Bitte erst anmelden.")
	}
	return false
}

// kontenSeite: GET /sso/konten.
func (s *SSO) kontenSeite(w http.ResponseWriter, r *http.Request) {
	if !s.kontenZugang(w, r) {
		return
	}
	s.kontenZeigen(w, r, r.URL.Query().Get("meldung"), r.URL.Query().Has("gut"))
}

func (s *SSO) kontenZeigen(w http.ResponseWriter, r *http.Request, meldung string, gut bool) {
	s.mu.RLock()
	namen := make([]string, 0, len(s.konten))
	for _, k := range s.konten {
		namen = append(namen, k.Name)
	}
	s.mu.RUnlock()
	ich, _ := s.Wer(r)

	var b strings.Builder
	if len(namen) == 0 {
		b.WriteString(`<p class="sub">Noch kein Konto. Solange es keins gibt und SSO aus ist, ist ` + ssoHTML(s.App) +
			` ohne Anmeldung offen. Das erste Konto schaltet die Anmeldung ein und meldet dich gleich damit an.</p>`)
	} else {
		b.WriteString(`<table>`)
		for _, n := range namen {
			b.WriteString(`<tr><td>` + ssoHTML(n))
			if n == ich.Nutzer && strings.HasPrefix(ich.Kennung, "konto:") {
				b.WriteString(` <span class="sub">(du)</span>`)
			}
			b.WriteString(`</td><td><form method="post" action="/sso/konten/loeschen"><input type="hidden" name="name" value="` +
				ssoHTML(n) + `"><button class="klein" type="submit">Löschen</button></form></td></tr>`)
		}
		b.WriteString(`</table>`)
	}
	b.WriteString(`<h2>Konto anlegen oder Passwort ändern</h2><form method="post" action="/sso/konten">` +
		`<label for="name">Benutzername</label><input id="name" name="name" autocomplete="off" autocapitalize="none" spellcheck="false" required pattern="[A-Za-z0-9][A-Za-z0-9._\-]{0,39}">` +
		`<label for="passwort">Passwort, mindestens ` + strconv.Itoa(ssoMinPasswort) + ` Zeichen</label><input id="passwort" name="passwort" type="password" autocomplete="new-password" required minlength="` + strconv.Itoa(ssoMinPasswort) + `">` +
		`<label for="wiederholung">Passwort wiederholen</label><input id="wiederholung" name="wiederholung" type="password" autocomplete="new-password" required>` +
		`<button class="knopf" type="submit">Speichern</button></form>` +
		`<p class="fuss"><a href="` + ssoHTML(s.Startseite) + `">Zurück zu ` + ssoHTML(s.App) + `</a></p>`)
	s.seite(w, ssoSeitenInhalt{Unterzeile: "Konten mit Benutzername und Passwort", Meldung: meldung, Gut: gut, Roh: b.String(), Breit: true})
}

// kontoSpeichern: POST /sso/konten legt an oder setzt ein neues Passwort.
func (s *SSO) kontoSpeichern(w http.ResponseWriter, r *http.Request) {
	if !ssoGleicheHerkunft(r) {
		s.fehlerSeite(w, "Die Anfrage kam nicht von dieser Seite.")
		return
	}
	if !s.kontenZugang(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
	if err := r.ParseForm(); err != nil {
		s.kontenZeigen(w, r, "Ungültige Angaben.", false)
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PostForm.Get("name")))
	passwort := r.PostForm.Get("passwort")
	switch {
	case !ssoNamensMuster.MatchString(name):
		s.kontenZeigen(w, r, "Der Benutzername darf nur Buchstaben, Ziffern, Punkt, Strich und Unterstrich enthalten (höchstens 40).", false)
		return
	case len([]rune(passwort)) < ssoMinPasswort:
		s.kontenZeigen(w, r, fmt.Sprintf("Das Passwort braucht mindestens %d Zeichen.", ssoMinPasswort), false)
		return
	case passwort != r.PostForm.Get("wiederholung"):
		s.kontenZeigen(w, r, "Die beiden Passwörter sind verschieden.", false)
		return
	}
	ich, vorher := s.Wer(r)
	neu := ssoKonto{Name: name, Hash: ssoPasswortHash(passwort)}
	s.mu.Lock()
	ersetzt := false
	for i := range s.konten {
		if s.konten[i].Name == name {
			s.konten[i] = neu
			ersetzt = true
		}
	}
	if !ersetzt {
		s.konten = append(s.konten, neu)
	}
	err := s.sichern()
	s.mu.Unlock()
	if err != nil {
		s.kontenZeigen(w, r, "Nicht gespeichert: "+err.Error(), false)
		return
	}
	log.Printf("sso: Konto %q %s", name, map[bool]string{true: "neues Passwort", false: "angelegt"}[ersetzt])
	// Wer bisher ohne Anmeldung hier war (erstes Konto) oder sein eigenes
	// Passwort ändert, bleibt angemeldet.
	if !vorher || ich.Kennung == "konto:"+name {
		s.lokalAnmelden(w, r, neu)
	}
	text := "Konto " + name + " angelegt."
	if ersetzt {
		text = "Neues Passwort für " + name + " gespeichert."
	}
	http.Redirect(w, r, "/sso/konten?gut=1&meldung="+url.QueryEscape(text), http.StatusSeeOther)
}

// kontoLoeschen: POST /sso/konten/loeschen.
func (s *SSO) kontoLoeschen(w http.ResponseWriter, r *http.Request) {
	if !ssoGleicheHerkunft(r) {
		s.fehlerSeite(w, "Die Anfrage kam nicht von dieser Seite.")
		return
	}
	if !s.kontenZugang(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<14)
	_ = r.ParseForm()
	name := strings.ToLower(strings.TrimSpace(r.PostForm.Get("name")))
	s.mu.Lock()
	rest := s.konten[:0:0]
	for _, k := range s.konten {
		if k.Name != name {
			rest = append(rest, k)
		}
	}
	weg := len(rest) != len(s.konten)
	s.konten = rest
	err := s.sichern()
	s.mu.Unlock()
	if err != nil {
		s.kontenZeigen(w, r, "Nicht gespeichert: "+err.Error(), false)
		return
	}
	if !weg {
		http.Redirect(w, r, "/sso/konten", http.StatusSeeOther)
		return
	}
	log.Printf("sso: Konto %q gelöscht", name)
	text := "Konto " + name + " gelöscht."
	if !s.Geschuetzt() {
		text += " Es gibt kein Konto mehr und SSO ist aus: " + s.App + " ist jetzt ohne Anmeldung offen."
	}
	http.Redirect(w, r, "/sso/konten?gut=1&meldung="+url.QueryEscape(text), http.StatusSeeOther)
}
