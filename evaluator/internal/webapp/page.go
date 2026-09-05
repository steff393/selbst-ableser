package webapp

import (
	"net/http"

	"selbst-ableser/internal/access"
)

// Base carries the fields every page's layout needs, regardless of what
// the page itself is about. Page-specific data structs embed it.
type Base struct {
	Title     string
	Session   *access.Session
	CSRFToken string
	Locked    bool // whether the master data vault is currently locked (STAMM-04)
	Flash     string
}

func (a *App) base(title string, sess *access.Session) Base {
	csrf := ""
	if sess != nil {
		csrf = sess.CSRFToken
	}
	return Base{
		Title:     title,
		Session:   sess,
		CSRFToken: csrf,
		Locked:    a.Vault.Locked(),
	}
}

// StateMessage renders UI-09's four distinguished states as a single
// small page, so every handler that hits one of them behaves identically
// instead of reinventing its own wording.
type StateMessage struct {
	Base
	Heading string
	Message string
}

const (
	msgLocked    = "Der Betreiber muss die Stammdaten erst entsperren."
	msgNoData    = "Für diesen Zeitraum liegen keine Daten vor."
	msgTechnical = "Es ist ein technischer Fehler aufgetreten."
)

func (a *App) renderLocked(w http.ResponseWriter, sess *access.Session) {
	a.render(w, "state.html", StateMessage{Base: a.base("Gesperrt", sess), Heading: "Gesperrt", Message: msgLocked})
}

func (a *App) renderNoData(w http.ResponseWriter, sess *access.Session) {
	a.render(w, "state.html", StateMessage{Base: a.base("Keine Daten", sess), Heading: "Keine Daten", Message: msgNoData})
}

func (a *App) renderExpired(w http.ResponseWriter, sess *access.Session, until string) {
	a.render(w, "state.html", StateMessage{
		Base:    a.base("Zugang abgelaufen", sess),
		Heading: "Zugang abgelaufen",
		Message: "Ihr Zugang galt bis " + until + ".",
	})
}

func (a *App) renderTechnicalError(w http.ResponseWriter, sess *access.Session, err error) {
	a.logger().Error("technical error", "err", err)
	w.WriteHeader(http.StatusInternalServerError)
	a.render(w, "state.html", StateMessage{Base: a.base("Fehler", sess), Heading: "Fehler", Message: msgTechnical})
}
