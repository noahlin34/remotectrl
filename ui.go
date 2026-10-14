package main

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// useColor is true only on a real terminal and when NO_COLOR is unset, so piped
// output stays free of escape codes.
var useColor = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}()

func sgr(code, s string) string {
	if !useColor {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func bold(s string) string   { return sgr("1", s) }
func dim(s string) string    { return sgr("2", s) }
func green(s string) string  { return sgr("32", s) }
func yellow(s string) string { return sgr("33", s) }

// pad right-pads by visible width so colored cells still align.
func pad(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func step(format string, a ...any) { fmt.Println(bold("==> " + fmt.Sprintf(format, a...))) }
