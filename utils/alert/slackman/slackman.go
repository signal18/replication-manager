package slackman

import (
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/bluele/logrus_slack"
	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/signal18/replication-manager/utils/meethelper"
	"github.com/sirupsen/logrus"
)

// SlackConfig holds the configuration for a Slack or Mattermost webhook.
type SlackConfig struct {
	HookType         string                 // Type of hook: "slack" or "meet" (Mattermost).
	URL              string                 // Webhook URL for sending messages.
	Channel          string                 // Target channel for notifications.
	User             string                 // Username used for sending messages.
	Icon             string                 // Emoji or image URL for the message sender.
	Timeout          time.Duration          // Timeout duration for webhook requests.
	AcceptedLevels   []logrus.Level         // Log levels that trigger notifications.
	AdditionalFields map[string]interface{} // Additional fields to include in the message.
}

// SlackmanHook represents an individual webhook configuration and its state.
type SlackmanHook struct {
	Config  SlackConfig // Hook configuration.
	hook    logrus.Hook // The actual logrus hook instance.
	Enabled bool        // Whether the hook is currently active.
	mu      sync.Mutex  // Mutex to ensure safe concurrent access.
}

// SlackManager manages multiple Slack/Mattermost webhook hooks.
type SlackManager struct {
	*logrus.Logger          // Embedded logrus logger.
	hook           sync.Map // A concurrent map storing hooks by their type.
}

// NewSlackManager initializes and returns a new SlackManager instance.
func NewSlackManager() *SlackManager {
	logger := logrus.New()
	manager := &SlackManager{
		Logger: logger,
	}

	return manager
}

// IsHookActive checks if a given hook type is active.
func (s *SlackManager) HasActiveHook() bool {
	var active bool
	s.hook.Range(func(key, value interface{}) bool {
		sh := value.(*SlackmanHook)
		if sh.Enabled {
			active = true
			return false
		}
		return true
	})

	return active
}

// IsHookActive checks if a given hook type is active.
func (s *SlackManager) IsHookActive(hooktype string) bool {
	sh := s.GetHook(hooktype)
	if sh == nil {
		return false
	}

	return sh.Enabled
}

// GetHook retrieves a hook by type, returning nil if it does not exist.
func (s *SlackManager) GetHook(hooktype string) *SlackmanHook {
	if v, ok := s.hook.Load(hooktype); ok {
		return v.(*SlackmanHook)
	}

	return nil
}

// LoadOrStoreHook retrieves an existing hook or creates a new one if it doesn't exist.
func (s *SlackManager) LoadOrStoreHook(hooktype string) *SlackmanHook {
	v, _ := s.hook.LoadOrStore(hooktype, &SlackmanHook{})
	return v.(*SlackmanHook)
}

// SetHookConfig sets or updates the configuration for a hook.
func (s *SlackManager) SetHookConfig(hooktype string, config SlackConfig) {
	sh := s.LoadOrStoreHook(hooktype)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	sh.Config = config

	// Set default log levels if none are specified.
	if len(sh.Config.AcceptedLevels) == 0 {
		sh.Config.AcceptedLevels = logrus.AllLevels
	}
}

// Activate enables a webhook hook by configuring and registering it.
func (s *SlackManager) Activate(hooktype string, useLock bool) bool {
	sh := s.GetHook(hooktype)
	if sh == nil {
		return false
	}

	if useLock {
		sh.mu.Lock()
		defer sh.mu.Unlock()
	}

	// Prevent activation if the hook is already enabled or has no URL.
	if sh.Enabled || sh.Config.URL == "" {
		return false
	}

	// Validate the webhook URL.
	_, err := url.Parse(sh.Config.URL)
	if err != nil {
		s.Errorf("Failed to parse URL: %v", err)
		return false
	}

	// Initialize the appropriate hook type (Slack or Mattermost).
	if sh.Config.HookType == "slack" {
		sh.hook = &logrus_slack.SlackHook{
			HookURL:        sh.Config.URL,
			AcceptedLevels: sh.Config.AcceptedLevels,
			Channel:        sh.Config.Channel,
			IconEmoji:      sh.Config.Icon,
			Username:       sh.Config.User,
			Timeout:        sh.Config.Timeout,
		}
	} else {
		sh.hook = &meethelper.MeetHook{
			WebhookURL:     sh.Config.URL,
			AcceptedLevels: sh.Config.AcceptedLevels,
			Timeout:        sh.Config.Timeout,
			FieldHeader:    "Replication Manager alert",
			Model: model.IncomingWebhookRequest{
				ChannelName: sh.Config.Channel,
				Username:    sh.Config.User,
				IconEmoji:   sh.Config.Icon,
			},
		}
	}

	// Register the hook with logrus.
	s.AddHook(sh.hook)
	sh.Enabled = true

	return true
}

// Deactivate removes and disables a webhook hook.
func (s *SlackManager) Deactivate(hooktype string, useLock bool) {
	sh := s.GetHook(hooktype)
	if sh == nil {
		return
	}

	if useLock {
		sh.mu.Lock()
		defer sh.mu.Unlock()
	}

	if !sh.Enabled || sh.hook == nil {
		return
	}

	// Remove the hook from logrus safely.
	newHooks := logrus.LevelHooks{}
	for level, hooks := range s.Hooks {
		filteredHooks := []logrus.Hook{}
		for _, hook := range hooks {
			if hook != sh.hook {
				filteredHooks = append(filteredHooks, hook)
			}
		}
		if len(filteredHooks) > 0 {
			newHooks[level] = filteredHooks
		}
	}

	// Replace the logger's hooks and disable the current hook.
	s.ReplaceHooks(newHooks)
	sh.hook = nil
	sh.Enabled = false
}

// UpdateConfig updates the configuration of an existing hook.
func (s *SlackManager) UpdateConfig(hooktype string, newConfig SlackConfig) {
	sh := s.LoadOrStoreHook(hooktype)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	// If the hook is enabled, deactivate it before updating.
	if sh.Enabled {
		s.Deactivate(hooktype, false)
	}

	// Set default log levels if not specified.
	if len(newConfig.AcceptedLevels) == 0 {
		newConfig.AcceptedLevels = logrus.AllLevels
	}

	// Activate the hook if a valid URL is provided.
	if newConfig.URL != "" {
		s.Activate(hooktype, false)
	}

	sh.Config = newConfig
}

// SetURL updates the webhook URL and reactivates the hook if necessary.
func (s *SlackManager) SetURL(hooktype, url string) {
	sh := s.LoadOrStoreHook(hooktype)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	if sh.Enabled {
		s.Deactivate(hooktype, false)
	}

	sh.Config.URL = url

	if url != "" {
		s.Activate(hooktype, false)
	}
}

func (s *SlackManager) SetAdditionalFields(hooktype string, fields map[string]interface{}) error {
	sh := s.GetHook(hooktype)
	if sh == nil {
		return fmt.Errorf("hook not found")
	}

	sh.Config.AdditionalFields = fields

	return nil
}

// SetChannel updates the webhook channel and reactivates the hook if necessary.
func (s *SlackManager) SetChannel(hooktype, channel string) {
	sh := s.LoadOrStoreHook(hooktype)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	if sh.Enabled {
		s.Deactivate(hooktype, false)
	}

	sh.Config.Channel = channel

	if sh.Config.URL != "" {
		s.Activate(hooktype, false)
	}
}

// SetUser updates the webhook username and reactivates the hook if necessary.
func (s *SlackManager) SetUser(hooktype, user string) {
	sh := s.LoadOrStoreHook(hooktype)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	if sh.Enabled {
		s.Deactivate(hooktype, false)
	}

	sh.Config.User = user

	if sh.Config.URL != "" {
		s.Activate(hooktype, false)
	}
}

// SetIcon updates the webhook icon and reactivates the hook if necessary.
func (s *SlackManager) SetIcon(hooktype, icon string) {
	sh := s.LoadOrStoreHook(hooktype)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	if sh.Enabled {
		s.Deactivate(hooktype, false)
	}

	sh.Config.Icon = icon

	if sh.Config.URL != "" {
		s.Activate(hooktype, false)
	}
}

// SetTimeout updates the webhook timeout and reactivates the hook if necessary.
func (s *SlackManager) SetTimeout(hooktype string, timeout time.Duration) {
	sh := s.LoadOrStoreHook(hooktype)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	if sh.Enabled {
		s.Deactivate(hooktype, false)
	}

	sh.Config.Timeout = timeout

	if sh.Config.URL != "" {
		s.Activate(hooktype, false)
	}
}

// SetAcceptedLevels updates the log levels that trigger webhook notifications.
func (s *SlackManager) SetAcceptedLevels(hooktype string, levels []logrus.Level) {
	sh := s.LoadOrStoreHook(hooktype)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	if sh.Enabled {
		s.Deactivate(hooktype, false)
	}

	sh.Config.AcceptedLevels = levels

	if sh.Config.URL != "" {
		s.Activate(hooktype, false)
	}
}
