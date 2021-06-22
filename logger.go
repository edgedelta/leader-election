package leaderelection

import "fmt"

// defaultLogger prints logs to stdout. It is the default logger used by leader election engine
// It implements the Logger interface.
type defaultLogger struct{}

// Log formats the message and prints to stdoud
func (l *defaultLogger) Log(format string, args ...interface{}) {
	fmt.Printf("%s\n", fmt.Sprintf(format, args...))
}
