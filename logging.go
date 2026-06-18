package logging

import (
	"github.com/go-logr/logr"
	"go.uber.org/zap/zapcore"
	zaplog "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Setup builds a logr.Logger backed by zap.
//
// level  is one of debug|info|warn|error.
// format is "json" for structured production logs, or "console" for
// human-readable development logs.
func Setup(level, format string) logr.Logger {
	lvl := parseLevel(level)
	development := format == "console"

	return zaplog.New(func(o *zaplog.Options) {
		o.Development = development
		o.Level = lvl
	})
}

func parseLevel(level string) zapcore.LevelEnabler {
	switch level {
	case "debug":
		return zapcore.DebugLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}
