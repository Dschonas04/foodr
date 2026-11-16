package main

import (
	"encoding/hex"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// Prüfvektoren für PBKDF2-HMAC-SHA256 (RFC 7914, Abschnitt 11, und die
// verbreiteten Werte für "password"/"salt").
func TestSSOPBKDF2(t *testing.T) {
	for _, f := range []struct {
		runden int
		soll   string
	}{
		{1, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{2, "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"},
		{4096, "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a"},
	} {
		if ist := hex.EncodeToString(ssoPBKDF2([]byte("password"), []byte("salt"), f.runden)); ist != f.soll {
			t.Fatalf("%d Runden: %s, erwartet %s", f.runden, ist, f.soll)
		}
	}
	h := ssoPasswortHash("geheim123")
	if !ssoPasswortPruefen(h, "geheim123") || ssoPasswortPruefen(h, "geheim124") {
		t.Fatal("Hash prüft falsch")
	}
}

func TestSSOKonten(t *testing.T) {
	s := NeuesSSO("Test", filepath.Join(t.TempDir(), "sso.json"), "TEST_SSO_AUS")
	m := http.NewServeMux()
	s.Wege(m, nil)
	m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("drin")) })
	app := httptest.NewServer(s.Schutz(m))
	defer app.Close()

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	fremd := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Ohne Konto und ohne SSO ist die App offen.
	if res, _ := fremd.Get(app.URL + "/"); res.StatusCode != http.StatusOK {
		t.Fatalf("offen erwartet, %d", res.StatusCode)
	}

	// Das erste Konto schaltet die Sperre ein und meldet gleich an.
	res, err := c.PostForm(app.URL+"/sso/konten", url.Values{"name": {"Jonas"}, "passwort": {"geheim123"}, "wiederholung": {"geheim123"}})
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("anlegen: %v %v", err, res.StatusCode)
	}
	if !s.Geschuetzt() {
		t.Fatal("nach dem ersten Konto muss die App gesperrt sein")
	}
	if res, _ := c.Get(app.URL + "/"); res.StatusCode != http.StatusOK {
		t.Fatalf("Anleger ist nicht angemeldet: %d", res.StatusCode)
	}
	if res, _ := fremd.Get(app.URL + "/"); res.StatusCode != http.StatusFound ||
		!strings.HasPrefix(res.Header.Get("Location"), "/sso/seite") {
		t.Fatalf("Fremder kommt herein: %d", res.StatusCode)
	}

	// Die Anmeldeseite zeigt die Maske.
	res, _ = fremd.Get(app.URL + "/sso/seite")
	buf := new(strings.Builder)
	_, _ = bufKopie(buf, res)
	if !strings.Contains(buf.String(), `name="passwort"`) {
		t.Fatal("keine Maske auf der Anmeldeseite")
	}

	// Falsches Passwort: 401, kein Keks.
	res, _ = fremd.PostForm(app.URL+"/sso/passwort", url.Values{"name": {"jonas"}, "passwort": {"falsch"}})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("falsches Passwort: %d", res.StatusCode)
	}
	// Unbekannter Name: dieselbe Antwort.
	res, _ = fremd.PostForm(app.URL+"/sso/passwort", url.Values{"name": {"niemand"}, "passwort": {"geheim123"}})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unbekannter Name: %d", res.StatusCode)
	}

	// Richtig: Weiterleitung mit Keks, danach herein.
	jar2, _ := cookiejar.New(nil)
	zweit := &http.Client{Jar: jar2}
	res, _ = zweit.PostForm(app.URL+"/sso/passwort", url.Values{"name": {"jonas"}, "passwort": {"geheim123"}, "ziel": {"/"}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("richtiges Passwort: %d", res.StatusCode)
	}

	// Ein neues Passwort beendet die alte Sitzung des zweiten Browsers.
	_, _ = c.PostForm(app.URL+"/sso/konten", url.Values{"name": {"jonas"}, "passwort": {"anders123"}, "wiederholung": {"anders123"}})
	zweitOhne := &http.Client{Jar: jar2, CheckRedirect: fremd.CheckRedirect}
	if res, _ := zweitOhne.Get(app.URL + "/"); res.StatusCode != http.StatusFound {
		t.Fatalf("alte Sitzung gilt noch: %d", res.StatusCode)
	}
	// Der Ändernde selbst bleibt drin.
	if res, _ := c.Get(app.URL + "/"); res.StatusCode != http.StatusOK {
		t.Fatalf("Ändernder fliegt raus: %d", res.StatusCode)
	}

	// Formular von fremder Seite wird abgewiesen.
	req, _ := http.NewRequest(http.MethodPost, app.URL+"/sso/passwort", strings.NewReader("name=jonas&passwort=anders123"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://boese.example")
	if res, _ := fremd.Do(req); res.StatusCode != http.StatusForbidden {
		t.Fatalf("fremde Herkunft: %d", res.StatusCode)
	}

	// Bremse: nach fünf Fehlversuchen von derselben Adresse 429, auch mit
	// richtigem Passwort.
	for i := 0; i < ssoFehlJeAdresse; i++ {
		_, _ = fremd.PostForm(app.URL+"/sso/passwort", url.Values{"name": {"jonas"}, "passwort": {"x"}})
	}
	res, _ = fremd.PostForm(app.URL+"/sso/passwort", url.Values{"name": {"jonas"}, "passwort": {"anders123"}})
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("Bremse greift nicht: %d", res.StatusCode)
	}

	// Letztes Konto gelöscht: wieder offen.
	_, _ = c.PostForm(app.URL+"/sso/konten/loeschen", url.Values{"name": {"jonas"}})
	if s.Geschuetzt() {
		t.Fatal("ohne Konto und SSO muss die App offen sein")
	}

	// Notaus schaltet auch die Konten ab.
	t.Setenv("TEST2_SSO_AUS", "1")
	s2 := NeuesSSO("Test", filepath.Join(t.TempDir(), "sso.json"), "TEST2_SSO_AUS")
	s2.konten = []ssoKonto{{Name: "a", Hash: ssoPasswortHash("12345678")}}
	if s2.Geschuetzt() {
		t.Fatal("Notaus muss die Sperre aufheben")
	}
}

func bufKopie(b *strings.Builder, res *http.Response) (int64, error) {
	defer res.Body.Close()
	var n int64
	p := make([]byte, 4096)
	for {
		k, err := res.Body.Read(p)
		b.Write(p[:k])
		n += int64(k)
		if err != nil {
			return n, nil
		}
	}
}
