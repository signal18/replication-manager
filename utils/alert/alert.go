package alert

import (
	"fmt"
	"strings"
	"time"

	"github.com/jordan-wright/email"
	"github.com/signal18/replication-manager/utils/alert/mailer"
)

type Alert struct {
	Instance  string
	State     string
	PrevState string
	Cluster   string
	Host      string
	Resolved  bool
}

// Text is the alert's message, as the alert email and the text channels carry it.
func (a *Alert) Text() string {
	host := ""
	if a.Host != "" {
		host = "Host: " + a.Host + "\n"
	}
	text := fmt.Sprintf("Alert: State changed from %s to %s\nMonitor: %s\nCluster: %s\n%s", a.PrevState, a.State, a.Instance, a.Cluster, host)
	if a.PrevState == "" {
		ts := time.Now().Format(time.RFC1123)
		if a.Resolved {
			text = fmt.Sprintf("Resolved: %s\nMonitor: %s\nCluster: %s\n%sTime: %s\n", a.State, a.Instance, a.Cluster, host, ts)
		} else {
			text = fmt.Sprintf("Alert: %s\nMonitor: %s\nCluster: %s\n%sTime: %s\n", a.State, a.Instance, a.Cluster, host, ts)
		}
	}
	return text
}

// Subject is the alert email's subject.
func (a *Alert) Subject() string {
	return fmt.Sprintf("Replication-Manager@%s Alert - Cluster %s state change detected", a.Instance, a.Cluster)
}

func (a *Alert) EmailMessage(to string, mailer *mailer.Mailer) error {
	e := email.NewEmail()
	e.From = mailer.From
	e.To = strings.Split(to, ",")
	e.Subject = a.Subject()
	e.Text = []byte(a.Text())

	err := mailer.Send(e)
	if err != nil {
		if strings.Contains(err.Error(), "no address") {
			return fmt.Errorf("to: %s, smtp: %s, user: %s, err: %v", to, mailer.Address, mailer.Username, err)
		}
		return err
	}

	return nil
}
