// Package logx holds the logging contract shared by the client and server
// packages. Both re-export it through type aliases, so their exported APIs
// stay independent while the contract and its defaults live in one place.
package logx

// Logger is the pluggable logging interface; it mirrors the standard
// library's log.Logger contract, so *log.Logger satisfies it directly.
type Logger interface {
	Println(v ...interface{})
	Printf(format string, v ...interface{})
}

// NOOPLogger discards all log output; it is the default logger.
type NOOPLogger struct{}

func (NOOPLogger) Println(v ...interface{})               {}
func (NOOPLogger) Printf(format string, v ...interface{}) {}

// OrNOOP returns logger, falling back to NOOPLogger when it is nil.
func OrNOOP(logger Logger) Logger {
	if logger == nil {
		return NOOPLogger{}
	}
	return logger
}
