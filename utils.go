package main

import (
	"io"
	"log"
	"strings"
)

// streamOutput streams the exec output to stdout/stderr (for real-time visibility)
func streamOutput(reader io.Reader, prefix string) {
	buf := make([]byte, 1024)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			for _, line := range strings.Split(string(buf[:n]), "\n") {
				if line != "" {
					log.Printf("%s: %s", prefix, line)
				}
			}
		}
		if err != nil {
			break
		}
	}
}

// truncate truncates a string to maxLen chars
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
