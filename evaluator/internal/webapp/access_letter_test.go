package webapp

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"selbst-ableser/internal/masterdata"
	"selbst-ableser/internal/qr"
	"selbst-ableser/internal/telegram"
)

func letterTestApp(t *testing.T) (*App, string) {
	t.Helper()
	app, mdPath := newTestApp(t)
	ended := telegram.Day("2020-12-31")
	md := masterdata.MasterData{
		Building: masterdata.Building{Name: "Musterweg 1"},
		Units: []masterdata.Unit{
			{ID: "u1", Name: "EG links", AreaM2: 50},
			{ID: "u2", Name: "OG rechts", AreaM2: 60},
		},
		Accesses: []masterdata.Access{
			{Token: "AAAA-BBBB-CCCC", UnitID: "u1", Start: "2024-01-01"},
			{Token: "DDDD-EEEE-FFFF", UnitID: "u2", Start: "2019-01-01", End: &ended},
		},
	}
	if err := masterdata.Save(mdPath, md, testPassword); err != nil {
		t.Fatalf("Save: %v", err)
	}
	app.NotifyConfig.BaseURL = "https://verbrauch.example.de/"
	return app, mdPath
}

func getLetters(t *testing.T, client *http.Client, target string) (*http.Response, string) {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

// One letter per grant that has not ended, with the configured address,
// the code, and a QR code for exactly the login link login.html reads the
// code back out of.
func TestAccessLettersForCurrentGrants(t *testing.T) {
	app, _ := letterTestApp(t)
	client, srv := operatorClient(t, app)

	resp, body := getLetters(t, client, srv.URL+"/operator/access/letters")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	for _, want := range []string{"Musterweg 1", "Wohnung EG links", "AAAA-BBBB-CCCC", "https://verbrauch.example.de<", "ab 01.01.2024"} {
		if !strings.Contains(body, want) {
			t.Errorf("letter lacks %q", want)
		}
	}
	if strings.Contains(body, "DDDD-EEEE-FFFF") {
		t.Error("a grant that has ended must get no letter")
	}
	code, err := qr.Encode("https://verbrauch.example.de/login#code=AAAA-BBBB-CCCC")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, code.SVG()) {
		t.Error("QR code does not encode the login link with the code in the fragment")
	}

	_, body = getLetters(t, client, srv.URL+"/operator/access/letters?unit=u2")
	if strings.Contains(body, "AAAA-BBBB-CCCC") || !strings.Contains(body, "Kein gültiger Zugang für diese Wohnung") {
		t.Error("the unit filter must leave out other units' letters")
	}
}

// Without a configured base address the letter falls back to the address
// the operator is using, and says so on screen.
func TestAccessLettersFallBackToRequestAddress(t *testing.T) {
	app, _ := letterTestApp(t)
	app.NotifyConfig.BaseURL = ""
	client, srv := operatorClient(t, app)

	_, body := getLetters(t, client, srv.URL+"/operator/access/letters")
	if !strings.Contains(body, srv.URL+"</dd>") {
		t.Errorf("letter does not carry the request's address %s", srv.URL)
	}
	if !strings.Contains(body, "unter der Sie gerade angemeldet sind") {
		t.Error("the fallback address must be pointed out")
	}
}

func TestAccessLettersAreOperatorOnly(t *testing.T) {
	app, mdPath := letterTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	client, srvURL := tenantClientFor(t, app, "AAAA-BBBB-CCCC")
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp, body := getLetters(t, client, srvURL+"/operator/access/letters")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Errorf("tenant got status %d, Location %q; want a redirect to /login", resp.StatusCode, resp.Header.Get("Location"))
	}
	if strings.Contains(body, "AAAA-BBBB-CCCC") {
		t.Error("a tenant must not see any access letter")
	}
}
