package clipboard

import (
	"bytes"
	"os/exec"
	"strings"
)

func Read() (string, error) {
	out, err := exec.Command("xsel", "--clipboard", "--output").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func Write(text string) error {
	cmd := exec.Command("xsel", "--clipboard", "--input")
	cmd.Stdin = bytes.NewBufferString(text)
	return cmd.Run()
}
