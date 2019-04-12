package out

import (
	"log"
)

type apmLogger struct {
	*log.Logger
}

func (l *apmLogger) Debugf(format string, args ...interface{}) {
	l.Printf("[debug] "+format, args...)
}

func (l *apmLogger) Errorf(format string, args ...interface{}) {
	l.Printf("[error] "+format, args...)
}

func (l *apmLogger) Error(err error) {
	if err != nil {
		l.Errorf(err.Error())
	}
}

func NewApmLogger(logger *log.Logger) *apmLogger {
	return &apmLogger{
		Logger: logger,
	}
}
