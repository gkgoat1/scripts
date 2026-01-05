package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	// Create a new scanner that reads from standard input
	scanner := bufio.NewScanner(os.Stdin)

	replace := os.Args[1]
	cmd := os.Args[2:]

	ch := make(chan struct{})
	n := 0

	// The Scan method blocks until a line is available, an error occurs, or EOF is reached.
	for scanner.Scan() {
		line := scanner.Text() // Get the current line of text

		// Process the line (e.g., print it, modify it, etc.)
		cmd2 := make([]string, len(cmd))
		for i, s := range cmd {
			cmd2[i] = strings.ReplaceAll(s, replace, line)
		}
		n += 1
		go func(cmd2 []string) {
			out, err := exec.Command(cmd2[0], cmd2[1:]...).CombinedOutput()
			if err != nil {
				fmt.Fprintln(os.Stderr, "command execution error:", err)
			}
			fmt.Print(string(out))
			ch <- struct{}{}
		}(cmd2)
	}

	// Check for errors after the loop
	if err := scanner.Err(); err != nil {
		// Note: io.EOF is not an error in this context and is handled by the loop termination.
		fmt.Fprintln(os.Stderr, "reading standard input:", err)
	}

	for i := 0; i < n; i++ {
		<-ch
	}
}
