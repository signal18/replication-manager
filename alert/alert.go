// mail.go

package alert

import (
	"fmt"
	"strings"

	"github.com/jordan-wright/email"
)

type Alert struct {
	From        string
	To          string
	Type        string
	Origin      string
	Destination string
}

func (a *Alert) Email() error {
	e := email.NewEmail()
	e.From = a.From
	e.To = strings.Split(a.To, ",")
	subj := fmt.Sprintf("MRM alert - State change detected on host %s", a.Origin)
	e.Subject = subj
	text := fmt.Sprintf(`MariaDB Replication manager has detected a change of state for host %s.
New server state is %s.`, a.Origin, a.Type)
	e.Text = []byte(text)
	err := e.Send(a.Destination, nil)
	return err
}
