package notify

import "selbst-ableser/internal/access"

// SendStartupNotification sends the "the system is running again"
// signal: on by default, but only sent when enabled is true
// (BENACHR-05). A service supervisor that restarts a crash-looping
// process would otherwise be able to generate unbounded mail.
func SendStartupNotification(sender Sender, audit *access.AuditLog, enabled bool, operatorEmail, baseURL string) error {
	if !enabled || operatorEmail == "" {
		return nil
	}
	body := "Das System wurde neu gestartet und muss nun vom Betreiber entsperrt werden.\n" + baseURL
	err := sender.Send(operatorEmail, "selbst-ableser: Neustart", body)
	if audit != nil {
		if aerr := audit.Record(access.Event{Type: access.EventNotificationSent, Detail: "startup"}); aerr != nil {
			return aerr
		}
	}
	return err
}
