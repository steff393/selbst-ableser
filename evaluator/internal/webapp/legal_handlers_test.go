package webapp

import (
	"net/http"
	"strings"
	"testing"
)

// TestLegalPagesFlagAnUnfilledTemplate: UI-12 wants these notices present
// when the interface is public, but presenting a template full of
// [Platzhalter] as if it were the real notice would be worse than saying
// plainly that it is not filled in yet.
func TestLegalPagesFlagAnUnfilledTemplate(t *testing.T) {
	app, _ := newTestApp(t)

	for _, path := range []string{"/impressum", "/datenschutz"} {
		resp, err := http.Get(publicServer(t, app) + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body := readAll(t, resp)
		if !strings.Contains(body, "noch nicht vollständig ausgefüllt") {
			t.Errorf("%s should say it is not filled in yet, got: %s", path, body)
		}
	}
}

// TestLegalPagesWorkWhileVaultLocked is config.Legal's whole reason for
// existing: unlike the rest of what an operator enters, UI-12's two
// notices must be readable before the vault has ever been unlocked in
// this run — the state every restart lands in — since a locked vault
// showing "not filled in" would read as the operator's own text having
// been lost.
func TestLegalPagesWorkWhileVaultLocked(t *testing.T) {
	app, _ := newTestApp(t) // never unlocked
	app.LegalConfig.ImprintText = "Erika Mustermann, Musterweg 1, 12345 Musterstadt"

	resp, err := http.Get(publicServer(t, app) + "/impressum")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := readAll(t, resp)
	if !strings.Contains(body, "Musterweg 1") {
		t.Errorf("expected the stored imprint despite the vault being locked, got: %s", body)
	}
}

// A finished text (no placeholders left) is shown as the notice itself.
func TestLegalPagesShowAFinishedText(t *testing.T) {
	app, _ := newTestApp(t)
	app.LegalConfig.ImprintText = "Erika Mustermann, Musterweg 1, 12345 Musterstadt"

	resp, err := http.Get(publicServer(t, app) + "/impressum")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body := readAll(t, resp)
	if !strings.Contains(body, "Musterweg 1") {
		t.Errorf("expected the stored imprint, got: %s", body)
	}
	if strings.Contains(body, "noch nicht vollständig ausgefüllt") {
		t.Error("a finished text must not be flagged as a draft")
	}
}

func TestHasPlaceholders(t *testing.T) {
	if !hasPlaceholders("Name: [Vorname Nachname]") {
		t.Error("an unfilled placeholder should be detected")
	}
	if hasPlaceholders("Erika Mustermann, Musterweg 1") {
		t.Error("a finished text has no placeholders")
	}
	if hasPlaceholders("") {
		t.Error("empty text has no placeholders (it is handled separately)")
	}
}

// TestLegalSaveStoresTexts: the two notices are edited on the
// Benachrichtigungen page and stored in config.Legal — its own save,
// independent of the masterdata building form (see handleLegalSave).
func TestLegalSaveStoresTexts(t *testing.T) {
	app, _ := newTestApp(t)
	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	postForm(t, client, srv.URL+"/operator/legal", map[string]string{
		"csrf_token":          sess.CSRFToken,
		"imprint_text":        "Erika Mustermann, Musterweg 1",
		"privacy_policy_text": "Verantwortlich: Erika Mustermann",
	})

	if app.LegalConfig.ImprintText != "Erika Mustermann, Musterweg 1" {
		t.Errorf("ImprintText = %q", app.LegalConfig.ImprintText)
	}
	if app.LegalConfig.PrivacyPolicyText != "Verantwortlich: Erika Mustermann" {
		t.Errorf("PrivacyPolicyText = %q", app.LegalConfig.PrivacyPolicyText)
	}
}

// TestBuildingSaveDoesNotTouchLegalTexts: the masterdata building form
// used to also carry imprint_text/privacy_policy_text (before they moved
// to config.Legal); a stray post of those fields to the old endpoint must
// be silently ignored, not leak into LegalConfig.
func TestBuildingSaveDoesNotTouchLegalTexts(t *testing.T) {
	app, mdPath := newTestApp(t)
	if err := app.Vault.Unlock(mdPath, testPassword); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	client, srv := operatorClient(t, app)
	sess := lookupSessionFromClient(t, app, client, srv)

	postForm(t, client, srv.URL+"/operator/masterdata/building", map[string]string{
		"csrf_token":           sess.CSRFToken,
		"name":                 "Musterhaus",
		"heating_kwh_per_unit": "1.4",
		"hot_water_kwh_per_m3": "58",
		"imprint_text":         "should be ignored",
		"privacy_policy_text":  "should be ignored",
	})

	md, _ := app.Vault.Get()
	if md.Building.Name != "Musterhaus" || md.Building.HeatingKWhPerUnit != 1.4 {
		t.Errorf("building settings were lost: %+v", md.Building)
	}
	if app.LegalConfig.ImprintText != "" {
		t.Errorf("the building form must not leak into LegalConfig, got %q", app.LegalConfig.ImprintText)
	}
}
