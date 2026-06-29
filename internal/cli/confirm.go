package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// confirm prompts the user with question on prompt, reads a line from in,
// and returns true on a leading 'y' or 'Y'. Any other reply (including
// empty / Ctrl-D) is treated as a "no" — destructive commands should bail
// rather than guess.
func confirm(in io.Reader, prompt io.Writer, question string) (bool, error) {
	if _, err := fmt.Fprintf(prompt, "%s [y/N] ", question); err != nil {
		return false, err
	}
	br := bufio.NewReader(in)
	line, err := br.ReadString('\n')
	if err != nil && line == "" {
		// EOF on an empty buffer is a "no".
		return false, nil
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return false, nil
	}
	switch line[0] {
	case 'y', 'Y':
		return true, nil
	}
	return false, nil
}
