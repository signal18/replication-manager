package slackman

import (
	"sync"
	"time"

	"github.com/bluele/logrus_slack"
	"github.com/sirupsen/logrus"
)

// SlackConfig holds the configuration for Slack logging.
type SlackConfig struct {
	URL            string
	Channel        string
	User           string
	Icon           string
	Timeout        time.Duration
	AcceptedLevels []logrus.Level
}

// SlackManager manages logging and Slack integration.
type SlackManager struct {
	*logrus.Logger
	Config  SlackConfig
	hook    *logrus_slack.SlackHook
	enabled bool
	mu      sync.Mutex
}

// NewSlackManager initializes the logger and Slack hook.
func NewSlackManager(config SlackConfig) *SlackManager {
	logger := logrus.New()

	// Set default log levels if none are provided.
	if len(config.AcceptedLevels) == 0 {
		config.AcceptedLevels = logrus.AllLevels
	}

	manager := &SlackManager{
		Logger: logger,
		Config: config,
	}

	// Automatically activate if a Slack URL is provided.
	if config.URL != "" {
		manager.Activate(true)
	}

	return manager
}

// Activate enables Slack logging.
func (s *SlackManager) Activate(useLock bool) {
	if useLock {
		s.mu.Lock()
		defer s.mu.Unlock()
	}

	if s.enabled || s.Config.URL == "" {
		return
	}

	s.hook = &logrus_slack.SlackHook{
		HookURL:        s.Config.URL,
		AcceptedLevels: s.Config.AcceptedLevels,
		Channel:        s.Config.Channel,
		IconEmoji:      s.Config.Icon,
		Username:       s.Config.User,
		Timeout:        s.Config.Timeout,
	}

	s.AddHook(s.hook)
	s.enabled = true
}

// Deactivate removes the Slack hook.
func (s *SlackManager) Deactivate(useLock bool) {
	if useLock {
		s.mu.Lock()
		defer s.mu.Unlock()
	}

	if !s.enabled || s.hook == nil {
		return
	}

	// Remove Slack hook safely.
	newHooks := logrus.LevelHooks{}
	for level, hooks := range s.Hooks {
		filteredHooks := []logrus.Hook{}
		for _, hook := range hooks {
			if hook != s.hook {
				filteredHooks = append(filteredHooks, hook)
			}
		}
		if len(filteredHooks) > 0 {
			newHooks[level] = filteredHooks
		}
	}

	s.ReplaceHooks(newHooks)
	s.hook = nil
	s.enabled = false
}

// UpdateConfig dynamically updates the Slack settings.
func (s *SlackManager) UpdateConfig(newConfig SlackConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Deactivate old Slack hook if enabled. Do not use lock since already locked.
	if s.enabled {
		s.Deactivate(false)
	}

	// Set default log levels if none are provided.
	if len(newConfig.AcceptedLevels) == 0 {
		newConfig.AcceptedLevels = logrus.AllLevels
	}

	// Update configuration.
	s.Config = newConfig

	// Reactivate with new config. Do not use lock since already locked.
	if newConfig.URL != "" {
		s.Activate(false)
	}
}

// SetURL updates the Slack webhook URL.
func (s *SlackManager) SetURL(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Deactivate old Slack hook if enabled. Do not use lock since already locked.
	if s.enabled {
		s.Deactivate(false)
	}

	s.Config.URL = url

	// Reactivate with new URL. Do not use lock since already locked.
	if url != "" {
		s.Activate(false)
	}
}

// SetChannel updates the Slack channel.
func (s *SlackManager) SetChannel(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.enabled {
		s.Deactivate(false)
	}

	s.Config.Channel = channel

	if s.Config.URL != "" {
		s.Activate(false)
	}
}

// SetUser updates the Slack username.
func (s *SlackManager) SetUser(user string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.enabled {
		s.Deactivate(false)
	}

	s.Config.User = user

	if s.Config.URL != "" {
		s.Activate(false)
	}
}

// SetIcon updates the Slack icon emoji.
func (s *SlackManager) SetIcon(icon string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.enabled {
		s.Deactivate(false)
	}

	s.Config.Icon = icon

	if s.Config.URL != "" {
		s.Activate(false)
	}
}

// SetTimeout updates the Slack timeout.
func (s *SlackManager) SetTimeout(timeout time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.enabled {
		s.Deactivate(false)
	}

	s.Config.Timeout = timeout

	if s.Config.URL != "" {
		s.Activate(false)
	}
}

// SetAcceptedLevels updates the log levels sent to Slack.
func (s *SlackManager) SetAcceptedLevels(levels []logrus.Level) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.enabled {
		s.Deactivate(false)
	}

	s.Config.AcceptedLevels = levels

	if s.Config.URL != "" {
		s.Activate(false)
	}
}
