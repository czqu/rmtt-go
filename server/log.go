package server

import "github.com/czqu/rmtt-go/internal/logx"

type (
	// Logger is the pluggable logging interface; it mirrors the standard
	// library's log.Logger contract, so *log.Logger satisfies it directly.
	Logger = logx.Logger

	// NOOPLogger discards all log output; it is the default logger.
	NOOPLogger = logx.NOOPLogger
)

// Package-level loggers, one per severity. All default to NOOPLogger; replace
// them with SetLogger or the per-level setters.
var (
	ERROR Logger = NOOPLogger{}
	INFO  Logger = NOOPLogger{}
	WARN  Logger = NOOPLogger{}
	DEBUG Logger = NOOPLogger{}
)

// SetLogger sets a single Logger implementation for all levels (error/info/warn/debug).
func SetLogger(logger Logger) {
	logger = logx.OrNOOP(logger)
	ERROR = logger
	INFO = logger
	WARN = logger
	DEBUG = logger
}

// SetDebugLogger sets the Logger used for debug-level messages only.
func SetDebugLogger(logger Logger) {
	DEBUG = logx.OrNOOP(logger)
}

// SetInfoLogger sets the Logger used for info-level messages only.
func SetInfoLogger(logger Logger) {
	INFO = logx.OrNOOP(logger)
}

// SetWarnLogger sets the Logger used for warn-level messages only.
func SetWarnLogger(logger Logger) {
	WARN = logx.OrNOOP(logger)
}

// SetErrorLogger sets the Logger used for error-level messages only.
func SetErrorLogger(logger Logger) {
	ERROR = logx.OrNOOP(logger)
}
