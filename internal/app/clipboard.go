package app

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"golang.design/x/clipboard"
)

type ClipboardCopyResult int

const (
	ClipboardCopied ClipboardCopyResult = iota
	ClipboardCopiedTerminalFallback
)

var clipboardReady bool

func InitClipboard() error {
	if err := clipboard.Init(); err != nil {
		clipboardReady = false
		return err
	}
	clipboardReady = true
	return nil
}

func CopyTextToClipboard(text string) (ClipboardCopyResult, error) {
	if clipboardReady {
		clipboard.Write(clipboard.FmtText, []byte(text))
		return ClipboardCopied, nil
	}

	if _, err := os.Stdout.WriteString(osc52Sequence(text)); err != nil {
		return ClipboardCopyResult(0), fmt.Errorf("clipboard unavailable and OSC52 fallback failed: %w", err)
	}

	return ClipboardCopiedTerminalFallback, nil
}

func osc52Sequence(text string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(text, "\x1b", "")))
	return "\x1b]52;c;" + encoded + "\x07"
}
