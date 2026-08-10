package server

type (
	// Logger is the pluggable logging interface used by the server library. It mirrors the
	// standard library's log.Logger contract, so *log.Logger satisfies it directly. Users can
	// install their own implementation per level via SetLogger/SetDebugLogger/... (see the
	// client package for the paho-style equivalent API).
	Logger interface {
		Println(v ...interface{})
		Printf(format string, v ...interface{})
	}

	NOOPLogger struct{}
)

func (NOOPLogger) Println(v ...interface{})               {}
func (NOOPLogger) Printf(format string, v ...interface{}) {}

var (
	ERROR Logger = NOOPLogger{}
	INFO  Logger = NOOPLogger{}
	WARN  Logger = NOOPLogger{}
	DEBUG Logger = NOOPLogger{}
)

// SetLogger sets a single Logger implementation for all levels (error/info/warn/debug).
func SetLogger(logger Logger) {
	if logger == nil {
		logger = NOOPLogger{}
	}
	ERROR = logger
	INFO = logger
	WARN = logger
	DEBUG = logger
}

// SetDebugLogger sets the Logger used for debug-level messages only.
func SetDebugLogger(logger Logger) {
	if logger == nil {
		logger = NOOPLogger{}
	}
	DEBUG = logger
}

// SetInfoLogger sets the Logger used for info-level messages only.
func SetInfoLogger(logger Logger) {
	if logger == nil {
		logger = NOOPLogger{}
	}
	INFO = logger
}

// SetWarnLogger sets the Logger used for warn-level messages only.
func SetWarnLogger(logger Logger) {
	if logger == nil {
		logger = NOOPLogger{}
	}
	WARN = logger
}

// SetErrorLogger sets the Logger used for error-level messages only.
func SetErrorLogger(logger Logger) {
	if logger == nil {
		logger = NOOPLogger{}
	}
	ERROR = logger
}
