// Package log: structured output with level filtering. Shared by all
// packages — acts as the hub file for testing graph expansion fan-out.
package log

import "fmt"

func Info(msg string)  { fmt.Println("[INFO]", msg) }
func Debug(msg string) { fmt.Println("[DEBUG]", msg) }
func Error(msg string, err error) {
	fmt.Println("[ERROR]", msg, err)
}
