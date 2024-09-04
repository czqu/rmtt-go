package RMTT

const (
	NET string = "[net]     "
	CLI string = "[client]     "
)

type (
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
