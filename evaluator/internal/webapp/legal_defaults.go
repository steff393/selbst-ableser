package webapp

import "strings"

// The two texts below are starting templates, not finished documents.
// Everything an operator must supply is written as [Platzhalter in eckigen
// Klammern]; hasPlaceholders finds any that are still there, so a
// half-filled text is flagged rather than quietly published.
//
// They are deliberately generic — no operator's name, address or contact
// appears anywhere in this source tree. The filled-in versions live in the
// encrypted master data, which never enters version control (SZ-6: the
// installation stores as little personal data as it can, and none of it
// belongs in the program).
//
// This is a drafting aid based on what this system actually processes, not
// legal advice; an operator publishing the interface should have the
// result checked.

const defaultImprintTemplate = `Angaben gemäß § 5 DDG

[Vorname Nachname]
[Straße Hausnummer]
[PLZ Ort]

Kontakt
Telefon: [Telefonnummer]
E-Mail: [E-Mail-Adresse]

Verantwortlich für den Inhalt
[Vorname Nachname], Anschrift wie oben

Diese Seite dient ausschließlich der Verbrauchsinformation für Mieterinnen und Mieter des
oben genannten Gebäudes. Es werden keine Waren oder Dienstleistungen angeboten.

Hinweis zur Streitbeilegung
Ich bin nicht bereit und nicht verpflichtet, an Streitbeilegungsverfahren vor einer
Verbraucherschlichtungsstelle teilzunehmen.`

const defaultPrivacyPolicyTemplate = `Datenschutzerklärung

1. Verantwortlicher
[Vorname Nachname]
[Straße Hausnummer]
[PLZ Ort]
E-Mail: [E-Mail-Adresse]

2. Zweck der Verarbeitung
Diese Anwendung stellt Ihnen die unterjährige Verbrauchsinformation zu Heizung und Wasser
für Ihre Wohnung bereit. Sie erfüllt damit die Pflicht aus § 6a Heizkostenverordnung.
Die angezeigten Werte sind keine Abrechnung.

3. Verarbeitete Daten
- Verbrauchswerte Ihrer Wohnung (Zählerstände und daraus berechnete Monatsverbräuche)
- Die Kennung Ihres Zugangscodes und der Zeitraum, für den er gilt
- Optional Ihre E-Mail-Adresse, ausschließlich für den Hinweis, dass eine neue
  Verbrauchsinformation vorliegt

Weitere personenbezogene Daten werden nicht gespeichert — insbesondere kein Name, keine
Anschrift und keine Angaben zu Ihrem Haushalt.

4. Rechtsgrundlagen
- Art. 6 Abs. 1 lit. c DSGVO (rechtliche Verpflichtung) für die Bereitstellung der
  Verbrauchsinformation nach § 6a Heizkostenverordnung
- Art. 6 Abs. 1 lit. b DSGVO (Vertragserfüllung) im Rahmen des Mietverhältnisses
- Art. 6 Abs. 1 lit. a DSGVO (Einwilligung) für den optionalen E-Mail-Hinweis; die
  Einwilligung können Sie jederzeit widerrufen

5. Gebäudevergleichswert
Die Anzeige enthält einen Vergleich Ihres flächenbezogenen Verbrauchs mit dem Durchschnitt
des Gebäudes. Dieser Durchschnitt ist ein zusammengefasster Wert; er wird nicht nach
einzelnen Wohnungen aufgeschlüsselt. Bei Gebäuden mit wenigen Wohnungen kann ein solcher
Durchschnitt gleichwohl Rückschlüsse zulassen. Die Angabe ist nach § 6a Heizkostenverordnung
vorgesehen.

6. Empfänger
Die Daten werden nicht an Dritte übermittelt. Die Anwendung wird
[auf eigener Hardware im Gebäude / bei folgendem Anbieter: Anbieter, Ort] betrieben.
[Falls ein Anbieter genutzt wird: Mit diesem besteht ein Vertrag zur Auftragsverarbeitung
nach Art. 28 DSGVO.]

7. Speicherdauer
Verbrauchswerte werden für die Dauer des Mietverhältnisses und darüber hinaus so lange
aufbewahrt, wie es für Abrechnung und gesetzliche Aufbewahrungsfristen erforderlich ist
[Zeitraum ergänzen, üblich sind bis zu zehn Jahre]. Ihr Zugang wird mit dem Ende des
Mietverhältnisses ungültig. Eine hinterlegte E-Mail-Adresse wird mit dem Zugang gelöscht.

8. Ihre Rechte
Sie haben das Recht auf Auskunft (Art. 15 DSGVO), Berichtigung (Art. 16), Löschung
(Art. 17), Einschränkung der Verarbeitung (Art. 18), Datenübertragbarkeit (Art. 20) und
Widerspruch (Art. 21). Wenden Sie sich dazu an die oben genannte Adresse.

Ihnen steht ferner ein Beschwerderecht bei einer Datenschutz-Aufsichtsbehörde zu, etwa bei
[zuständige Landesdatenschutzbehörde, Ort].

9. Keine Weitergabe an Analysedienste
Die Anwendung bindet keine externen Schriftarten, Analyse- oder Werbedienste ein und setzt
keine Cookies außer einem technisch notwendigen Sitzungs-Cookie für die Anmeldung.

Stand: [Datum]`

// hasPlaceholders reports whether text still contains an unfilled
// [Platzhalter]. Used to keep a template that was saved unedited from
// being presented to tenants as a finished legal notice.
func hasPlaceholders(text string) bool {
	open := strings.Index(text, "[")
	if open == -1 {
		return false
	}
	return strings.Index(text[open:], "]") != -1
}
