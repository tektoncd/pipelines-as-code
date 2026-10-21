package resolve

import "go.uber.org/zap"

func debugf(log *zap.SugaredLogger, format string, args ...any) {
	if log == nil {
		return
	}
	log.Debugf(format, args...)
}

func warnf(log *zap.SugaredLogger, format string, args ...any) {
	if log != nil {
		log.Warnf(format, args...)
	}
}

func logInfo(log *zap.SugaredLogger, msg string) {
	if log == nil {
		return
	}
	log.Info(msg)
}
