package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ssoTestAnbieter spielt einen OIDC-Aussteller nach.
func ssoTestAnbieter(t *testing.T, email string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	codes := map[string][2]string{} // code -> nonce, challenge
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": srv.URL, "authorization_endpoint": srv.URL + "/auth",
				"token_endpoint": srv.URL + "/token", "end_session_endpoint": srv.URL + "/logout"})
		case "/auth":
			q := r.URL.Query()
			if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "app" {
				http.Error(w, "falsch", 400)
				return
			}
			codes["c1"] = [2]string{q.Get("nonce"), q.Get("code_challenge")}
			http.Redirect(w, r, q.Get("redirect_uri")+"?code=c1&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
		case "/token":
			_ = r.ParseForm()
			id, pw, _ := r.BasicAuth()
			c, ok := codes[r.Form.Get("code")]
			h := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || id != "app" || pw != "geheim" || base64.RawURLEncoding.EncodeToString(h[:]) != c[1] {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			nutzlast, _ := json.Marshal(map[string]any{"iss": srv.URL, "aud": "app", "exp": time.Now().Add(time.Minute).Unix(),
				"nonce": c[0], "sub": "u1", "email": email, "email_verified": true, "preferred_username": "jonas", "name": "Jonas"})
			_ = json.NewEncoder(w).Encode(map[string]string{"id_token": "e30." + base64.RawURLEncoding.EncodeToString(nutzlast) + ".x"})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSSOAblauf(t *testing.T) {
	anbieter := ssoTestAnbieter(t, "jonas@example.org")
	s := NeuesSSO("Test", filepath.Join(t.TempDir(), "sso.json"), "TEST_SSO_AUS")
	m := http.NewServeMux()
	s.Wege(m, nil)
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("drin")) })
	app := httptest.NewServer(s.Schutz(m))
	defer app.Close()

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}

	// Aus: alles offen.
	if r, _ := c.Get(app.URL + "/api/x"); r.StatusCode != 200 {
		t.Fatalf("ohne SSO gesperrt: %d", r.StatusCode)
	}
	// Einschalten mit unerreichbarem Aussteller scheitert.
	put := func(k map[string]any) int {
		roh, _ := json.Marshal(k)
		req, _ := http.NewRequest(http.MethodPut, app.URL+"/api/sso/einstellungen", strings.NewReader(string(roh)))
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r.StatusCode
	}
	if code := put(map[string]any{"aktiv": true, "aussteller": "http://127.0.0.1:1", "clientId": "app"}); code != 400 {
		t.Fatalf("kaputter Aussteller angenommen: %d", code)
	}
	if code := put(map[string]any{"aktiv": true, "aussteller": anbieter.URL, "clientId": "app", "geheimnis": "geheim", "erlaubt": "jonas@example.org"}); code != 200 {
		t.Fatalf("einschalten: %d", code)
	}
	// Geheimnis wird nicht herausgegeben.
	r, _ := c.Get(app.URL + "/api/sso/einstellungen")
	if r.StatusCode != 401 {
		t.Fatalf("Einstellungen ohne Anmeldung lesbar: %d", r.StatusCode)
	}
	// API gesperrt, Seite leitet zur Anmeldung, die führt über den Anbieter zurück.
	if r, _ := c.Get(app.URL + "/api/x"); r.StatusCode != 401 {
		t.Fatalf("API offen: %d", r.StatusCode)
	}
	r, err := c.Get(app.URL + "/rezepte?x=1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Request.URL.Path != "/rezepte" || r.StatusCode != 200 {
		t.Fatalf("nach Anmeldung bei %s gelandet (%d)", r.Request.URL, r.StatusCode)
	}
	r, _ = c.Get(app.URL + "/api/sso/status")
	var st struct {
		Angemeldet bool      `json:"angemeldet"`
		Nutzer     SSONutzer `json:"nutzer"`
	}
	_ = json.NewDecoder(r.Body).Decode(&st)
	if !st.Angemeldet || st.Nutzer.Email != "jonas@example.org" {
		t.Fatalf("Status: %+v", st)
	}
	r, _ = c.Get(app.URL + "/api/sso/einstellungen")
	var e map[string]any
	_ = json.NewDecoder(r.Body).Decode(&e)
	if strings.Contains(r.Status, "200") == false || e["geheimnisDa"] != true || strings.Contains(func() string { b, _ := json.Marshal(e); return string(b) }(), "geheim\"") {
		t.Fatalf("Einstellungen: %v", e)
	}
	// Abmelden löscht die Sitzung.
	nc := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r, _ := nc.Get(app.URL + "/sso/abmelden"); r.StatusCode != 302 || !strings.HasPrefix(r.Header.Get("Location"), anbieter.URL+"/logout") {
		t.Fatalf("Abmelden: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r, _ := c.Get(app.URL + "/api/x"); r.StatusCode != 401 {
		t.Fatalf("nach Abmelden offen: %d", r.StatusCode)
	}
}

func TestSSONichtFreigegeben(t *testing.T) {
	anbieter := ssoTestAnbieter(t, "fremd@example.org")
	s := NeuesSSO("Test", filepath.Join(t.TempDir(), "sso.json"), "TEST_SSO_AUS")
	s.k = SSOKonfig{Aktiv: true, Aussteller: anbieter.URL, ClientID: "app", Geheimnis: "geheim", Erlaubt: "jonas@example.org"}
	m := http.NewServeMux()
	s.Wege(m, nil)
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("drin")) })
	app := httptest.NewServer(s.Schutz(m))
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	r, _ := (&http.Client{Jar: jar}).Get(app.URL + "/")
	if r.StatusCode != 403 {
		t.Fatalf("fremdes Konto zugelassen: %d", r.StatusCode)
	}
}

func TestSSOZielUndNotaus(t *testing.T) {
	for _, z := range []string{"//boese.de", "https://boese.de", "/\\boese", "/sso/zurueck"} {
		if ssoZiel(z) != "" {
			t.Fatalf("%q durchgelassen", z)
		}
	}
	if ssoZiel("/woche?x=1") != "/woche?x=1" {
		t.Fatal("eigener Pfad verworfen")
	}
	t.Setenv("TEST_SSO_AUS", "1")
	s := NeuesSSO("Test", filepath.Join(t.TempDir(), "sso.json"), "TEST_SSO_AUS")
	s.k = SSOKonfig{Aktiv: true, Aussteller: "x", ClientID: "y"}
	if s.An() {
		t.Fatal("Notaus wirkt nicht")
	}
}

func TestSSOFreigabe(t *testing.T) {
	n := SSONutzer{Kennung: "u1", Email: "a@b.de", Nutzer: "jonas"}
	if ssoErlaubt("a@b.de", n) {
		t.Fatal("unbestätigte Adresse zugelassen")
	}
	if ssoErlaubt("a@b.de", SSONutzer{Nutzer: "a@b.de"}) {
		t.Fatal("Nutzername als Adresse zugelassen")
	}
	n.EmailBestaetigt = true
	if !ssoErlaubt("x@y.de, a@b.de", n) || !ssoErlaubt("Jonas", n) || !ssoErlaubt("u1", n) || ssoErlaubt("anna", n) {
		t.Fatal("Freigabeliste falsch ausgewertet")
	}
	for _, z := range []string{"/\tx", "/a\nb", "/x\x00"} {
		if ssoZiel(z) != "" {
			t.Fatalf("%q durchgelassen", z)
		}
	}
	s := NeuesSSO("Test", filepath.Join(t.TempDir(), "sso.json"), "TEST_SSO_AUS")
	if _, err := s.anbieter(context.Background(), "http://keycloak.example:8080/realms/x"); err == nil {
		t.Fatal("http-Aussteller angenommen")
	}
}

func TestSSOSitzungVerfaellt(t *testing.T) {
	s := NeuesSSO("Test", filepath.Join(t.TempDir(), "sso.json"), "TEST_SSO_AUS")
	s.k = SSOKonfig{Aktiv: true, Aussteller: "https://a", ClientID: "app"}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	s.setzen(w, r, ssoKeks, ssoSitzung{SSONutzer: SSONutzer{Kennung: "u1", Nutzer: "jonas"}, Aussteller: "https://a", ClientID: "app",
		Bis: time.Now().Add(time.Hour).Unix()}, time.Hour)
	r.AddCookie(w.Result().Cookies()[0])
	if _, ok := s.Wer(r); !ok {
		t.Fatal("gültige Sitzung abgelehnt")
	}
	s.k.Erlaubt = "anna"
	if _, ok := s.Wer(r); ok {
		t.Fatal("Sitzung überlebt geänderte Freigabe")
	}
	s.k.Erlaubt = ""
	s.k.ClientID = "andere"
	if _, ok := s.Wer(r); ok {
		t.Fatal("Sitzung überlebt anderen Client")
	}
}

func TestSSOGeheimnisBleibtBeimAnbieter(t *testing.T) {
	a := ssoTestAnbieter(t, "x@y.de")
	b := ssoTestAnbieter(t, "x@y.de")
	s := NeuesSSO("Test", filepath.Join(t.TempDir(), "sso.json"), "TEST_SSO_AUS")
	s.k = SSOKonfig{Aussteller: a.URL, ClientID: "app", Geheimnis: "geheim"}
	m := http.NewServeMux()
	s.Wege(m, nil)
	put := func(k string) {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/sso/einstellungen", strings.NewReader(k)))
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	put(`{"aussteller":"` + a.URL + `","clientId":"app","geheimnis":""}`)
	if s.k.Geheimnis != "geheim" {
		t.Fatal("Geheimnis beim selben Anbieter verloren")
	}
	put(`{"aussteller":"` + b.URL + `","clientId":"app","geheimnis":""}`)
	if s.k.Geheimnis != "" {
		t.Fatal("Geheimnis zu anderem Anbieter mitgenommen")
	}
}
