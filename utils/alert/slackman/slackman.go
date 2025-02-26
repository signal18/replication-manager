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

type SlackmanHook struct {
	Config  SlackConfig
	hook    *logrus_slack.SlackHook // Webhook URL
	Enabled bool
	mu      sync.Mutex
}

// SlackManager manages logging and Slack integration.
type SlackManager struct {
	*logrus.Logger
	hook sync.Map // Slack hooks
}

// NewSlackManager initializes the logger and Slack hook.
func NewSlackManager() *SlackManager {
	logger := logrus.New()
	manager := &SlackManager{
		Logger: logger,
	}

	return manager
}

func (s *SlackManager) GetHook(hooktype string) *SlackmanHook {
	if v, ok := s.hook.Load(hooktype); ok {
		return v.(*SlackmanHook)
	}

	return nil
}

func (s *SlackManager) LoadOrStoreHook(hooktype string) *SlackmanHook {
	v, _ := s.hook.LoadOrStore(hooktype, &SlackmanHook{})
	return v.(*SlackmanHook)
}

// NewSlackManager initializes the logger and Slack hook.
func (s *SlackManager) SetHookConfig(hooktype string, config SlackConfig) {
	sh := s.LoadOrStoreHook(hooktype)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	sh.Config = config

	// Set default log levels if none are provided.
	if len(sh.Config.AcceptedLevels) == 0 {
		sh.Config.AcceptedLevels = logrus.AllLevels
	}
}

// Activate enables Slack logging.
func (s *SlackManager) Activate(hooktype string, useLock bool) bool {
	sh := s.GetHook(hooktype)
	if sh == nil {
		return false
	}

	if useLock {
		sh.mu.Lock()
		defer sh.mu.Unlock()
	}

	if sh.Enabled || sh.Config.URL == "" {
		return false
	}

	sh.hook = &logrus_slack.SlackHook{
		HookURL:        sh.Config.URL,
		AcceptedLevels: sh.Config.AcceptedLevels,
		Channel:        sh.Config.Channel,
		IconEmoji:      sh.Config.Icon,
		Username:       sh.Config.User,
		Timeout:        sh.Config.Timeout,
	}

	s.AddHook(sh.hook)

	sh.Enabled = true

	return true
}

// Deactivate removes the Slack hook.
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

	// Remove Slack hook safely.
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

	s.ReplaceHooks(newHooks)
	sh.hook = nil
	sh.Enabled = false
}

// UpdateConfig dynamically updates the Slack settings.
func (s *SlackManager) UpdateConfig(hooktype string, newConfig SlackConfig) {
	sh := s.LoadOrStoreHook(hooktype)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	// Deactivate old Slack hook if enabled. Do not use lock since already locked.
	if sh.Enabled {
		s.Deactivate(hooktype, false)
	}

	// Set default log levels if none are provided.
	if len(newConfig.AcceptedLevels) == 0 {
		newConfig.AcceptedLevels = logrus.AllLevels
	}

	// Reactivate with new config. Do not use lock since already locked.
	if newConfig.URL != "" {
		s.Activate(hooktype, false)
	}

	sh.Config = newConfig
}

// SetURL updates the Slack webhook URL.
func (s *SlackManager) SetURL(hooktype, url string) {
	sh := s.LoadOrStoreHook(hooktype)

	sh.mu.Lock()
	defer sh.mu.Unlock()
	// Deactivate old Slack hook if enabled. Do not use lock since already locked.
	if sh.Enabled {
		s.Deactivate(hooktype, false)
	}

	sh.Config.URL = url

	// Reactivate with new URL. Do not use lock since already locked.
	if url != "" {
		s.Activate(hooktype, false)
	}
}

// SetChannel updates the Slack channel.
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

// SetUser updates the Slack username.
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

// SetIcon updates the Slack icon emoji.
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

// SetTimeout updates the Slack timeout.
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

// SetAcceptedLevels updates the log levels sent to Slack.
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
