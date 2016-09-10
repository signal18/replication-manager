// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

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
