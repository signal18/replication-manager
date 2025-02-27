package alert

import (
	"fmt"
	"strings"

	"github.com/jordan-wright/email"
	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/signal18/replication-manager/utils/alert/mailer"
	"github.com/signal18/replication-manager/utils/meethelper"
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

	return mailer.Send(e)
}

func (a *Alert) PostMeetMessage(meetClient *meethelper.MeetChatClient, channelID string) error {
	if channelID == "" {
		return fmt.Errorf("Channel ID is required")
	}

	post := model.Post{
		UserId:    meetClient.UserID,
		ChannelId: channelID,
	}

	host := ""
	if a.Host != "" {
		host = "Host: " + a.Host + "\n"
	}

	msg := fmt.Sprintf("Alert: State changed from %s to %s\nMonitor: %s\nCluster: %s\n%s", a.PrevState, a.State, a.Instance, a.Cluster, host)
	if a.PrevState == "" {
		if a.Resolved {
			msg = fmt.Sprintf("Resolved: %s\nMonitor: %s\nCluster: %s\n%s", a.State, a.Instance, a.Cluster, host)
		} else {
			msg = fmt.Sprintf("Alert: %s\nMonitor: %s\nCluster: %s\n%s", a.State, a.Instance, a.Cluster, host)
		}
	}
	post.Message = msg

	_, err := meetClient.PostAdvancedMessage(&post, false)
	if err != nil {
		return err
	}

	return nil
}
