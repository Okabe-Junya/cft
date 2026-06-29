package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfirm(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false}, // empty
		{"", false},   // EOF
		{"maybe\n", false},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			var prompt bytes.Buffer
			got, err := confirm(strings.NewReader(c.input), &prompt, "delete?")
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != c.want {
				t.Errorf("input=%q got=%v want=%v", c.input, got, c.want)
			}
			if !strings.Contains(prompt.String(), "delete?") {
				t.Errorf("prompt missing question: %q", prompt.String())
			}
		})
	}
}
