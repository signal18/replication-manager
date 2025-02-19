package alert

import (
	"fmt"
	"log"
	"strings"

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

func (a *Alert) EmailMessage(to string, mailer *mailer.Mailer) error {
	e := email.NewEmail()
	e.From = mailer.From
	e.To = strings.Split(to, ",")

	host := ""
	if a.Host != "" {
		host = "Host: " + a.Host + "\n"
	}

	e.Subject = fmt.Sprintf("Replication-Manager@%s Alert - Cluster %s state change detected", a.Instance, a.Cluster)
	text := fmt.Sprintf("Alert: State changed from %s to %s\nMonitor: %s\nCluster: %s\n%s", a.PrevState, a.State, a.Instance, a.Cluster, host)
	if a.PrevState == "" {
		if a.Resolved {
			text = fmt.Sprintf("Resolved: %s\nMonitor: %s\nCluster: %s\n%s", a.State, a.Instance, a.Cluster, host)
		} else {
			text = fmt.Sprintf("Alert: %s\nMonitor: %s\nCluster: %s\n%s", a.State, a.Instance, a.Cluster, host)
		}
	}
	e.Text = []byte(text)

	var err error
	if mailer.TLS != nil {
		err = mailer.SendWithTLS(e)
	} else {
		err = mailer.Send(e)
	}

	if err != nil {
		log.Printf("ERROR: Could not send mail alert: %s", err)
	}
	return err
}
