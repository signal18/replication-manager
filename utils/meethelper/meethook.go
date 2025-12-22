package meethelper

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/sirupsen/logrus"
)

type MeetHook struct {
	WebhookURL     string
	AcceptedLevels []logrus.Level
	Timeout        time.Duration
	Async          bool
	FieldHeader    string
	Model          model.IncomingWebhookRequest

	hook *WebHook
}

var ErrTimeout = errors.New("Request timed out")

func (sh *MeetHook) Fire(e *logrus.Entry) error {
	if sh.hook == nil {
		sh.hook = NewWebHook(sh.WebhookURL)
	}

	if !slices.Contains(sh.AcceptedLevels, e.Level) {
		return nil
	}

	payload := sh.Model
	color := LevelColorMap[e.Level]

	attachment := model.SlackAttachment{}
	payload.Attachments = []*model.SlackAttachment{&attachment}

	if len(e.Data) > 0 {
		attachment.Text = sh.FieldHeader

		for k, v := range e.Data {
			attachment.Fields = append(attachment.Fields, sh.makeAttachmentField(k, v))
		}

		attachment.Pretext = e.Message
	} else {
		attachment.Text = e.Message
	}

	attachment.Fallback = e.Message
	attachment.Color = color

	if sh.Async {
		go sh.postMessage(&payload)
		return nil
	}

	return sh.postMessage(&payload)
}

func (sh *MeetHook) postMessage(payload *model.IncomingWebhookRequest) error {
	if sh.Timeout <= 0 {
		return sh.hook.PostWebhookMessage(payload)
	}

	ech := make(chan error, 1)
	go func(ch chan error) {
		ch <- nil
		ch <- sh.hook.PostWebhookMessage(payload)
	}(ech)
	<-ech

	select {
	case err := <-ech:
		return err
	case <-time.After(sh.Timeout):
		return ErrTimeout
	}
}

// Levels sets which levels to sent to slack
func (sh *MeetHook) Levels() []logrus.Level {
	if sh.AcceptedLevels == nil {
		return AllLevels
	}
	return sh.AcceptedLevels
}

var LevelColorMap = map[logrus.Level]string{
	logrus.DebugLevel: "#9B30FF",
	logrus.InfoLevel:  "good",
	logrus.WarnLevel:  "warning",
	logrus.ErrorLevel: "danger",
	logrus.FatalLevel: "danger",
	logrus.PanicLevel: "danger",
}

// Supported log levels
var AllLevels = []logrus.Level{
	logrus.DebugLevel,
	logrus.InfoLevel,
	logrus.WarnLevel,
	logrus.ErrorLevel,
	logrus.FatalLevel,
	logrus.PanicLevel,
}

// LevelThreshold - Returns every logging level above and including the given parameter.
func LevelThreshold(l logrus.Level) []logrus.Level {
	for i := range AllLevels {
		if AllLevels[i] == l {
			return AllLevels[i:]
		}
	}
	return []logrus.Level{}
}

type WebHook struct {
	hookURL string
}

func NewWebHook(hookURL string) *WebHook {
	return &WebHook{hookURL}
}

func (wh *WebHook) PostWebhookMessage(payload *model.IncomingWebhookRequest) error {

	values, err := json.Marshal(payload)
	if err != nil {
		fmt.Println("PostWebhookMessage: cannot marshal payload:", err)
		return err
	}

	req, err := http.NewRequest("POST", wh.hookURL, bytes.NewBuffer(values))
	if err != nil {
		return fmt.Errorf("failed to create POST request: %v", err)
	}

	client := &http.Client{}

	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed POST request to %s: %v", wh.hookURL, err)
	}
	defer resp.Body.Close()

	t, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != 200 {
		return errors.New(string(t))
	}

	return nil
}

func (sh *MeetHook) makeAttachmentField(title string, value interface{}) *model.SlackAttachmentField {
	field := &model.SlackAttachmentField{}

	field.Title = title
	if str, ok := value.(string); ok {
		field.Value = str
	} else {
		field.Value = fmt.Sprint(value)
	}

	if len(field.Value.(string)) <= 20 {
		field.Short = true
	}
	return field
}
