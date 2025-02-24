package config

import (
	"github.com/sirupsen/logrus"
)

type LogrusWrapper struct {
	configs map[string]*Config
	*logrus.Logger
}

func NewLogrusWrapper(conf *Config, logger *logrus.Logger) *LogrusWrapper {
	lw := &LogrusWrapper{
		configs: make(map[string]*Config),
		Logger:  logger,
	}

	lw.configs["default"] = conf

	return lw
}

func (l *LogrusWrapper) UpdateConfig(clustername string, conf *Config) {
	l.configs[clustername] = conf
}

func (l *LogrusWrapper) GetConfig(clustername string) *Config {
	cnf, ok := l.configs[clustername]
	if !ok {
		return l.configs["default"]
	}

	return cnf
}

func (l *LogrusWrapper) Infof(clustername string, module int, format string, args ...interface{}) {
	l.GetConfig(clustername).IsEligibleForPrinting(module, LvlInfo)
	l.Logger.WithFields(logrus.Fields{"cluster": clustername, "module": GetTagsForLog(module)}).Infof(format, args...)
}

func (l *LogrusWrapper) Debugf(clustername string, module int, format string, args ...interface{}) {
	l.GetConfig(clustername).IsEligibleForPrinting(module, LvlDbg)
	l.Logger.WithFields(logrus.Fields{"cluster": clustername, "module": GetTagsForLog(module)}).Debugf(format, args...)
}

func (l *LogrusWrapper) Errorf(clustername string, module int, format string, args ...interface{}) {
	l.GetConfig(clustername).IsEligibleForPrinting(module, LvlErr)
	l.Logger.WithFields(logrus.Fields{"cluster": clustername, "module": GetTagsForLog(module)}).Errorf(format, args...)
}

func (l *LogrusWrapper) Warnf(clustername string, module int, format string, args ...interface{}) {
	l.GetConfig(clustername).IsEligibleForPrinting(module, LvlWarn)
	l.Logger.WithFields(logrus.Fields{"cluster": clustername, "module": GetTagsForLog(module)}).Warnf(format, args...)
}
