package webapp

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestBuildingSaveUpdatesConversionFactors(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)

	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/masterdata/building", url.Values{
		"csrf_token":           {sess.CSRFToken},
		"name":                 {"Musterhaus 12"},
		"heating_kwh_per_unit": {"1.42"},
		"hot_water_kwh_per_m3": {"124.3"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	md, ok := app.Vault.Get()
	if !ok {
		t.Fatal("vault unexpectedly locked after save")
	}
	if md.Building.Name != "Musterhaus 12" || md.Building.HeatingKWhPerUnit != 1.42 || md.Building.HotWaterKWhPerM3 != 124.3 {
		t.Errorf("Building = %+v, want the submitted values", md.Building)
	}
}

func TestBuildingSaveRejectsInvalidFactor(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	loginAsOperator(t, client, srv.URL)

	sess := lookupSession(t, app, jar, srv.URL)

	resp, err := client.PostForm(srv.URL+"/operator/masterdata/building", url.Values{
		"csrf_token":           {sess.CSRFToken},
		"name":                 {"Musterhaus 12"},
		"heating_kwh_per_unit": {"not-a-number"},
		"hot_water_kwh_per_m3": {"124.3"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	md, ok := app.Vault.Get()
	if !ok {
		t.Fatal("vault unexpectedly locked")
	}
	if md.Building.Name == "Musterhaus 12" {
		t.Error("an invalid factor should have rejected the whole save, not applied the name change")
	}
}
