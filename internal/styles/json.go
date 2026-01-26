package styles

import (
	"bytes"
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

var jsonLexer = lexers.Get("json")
var terminalFormatter = formatters.Get("terminal256")
var jsonStyle = styles.Get("monokai")

func FormatJSONCell(body []byte, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	text := normalizeWhitespace(string(body))

	return highlightJSON(text)
}

func normalizeWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\t", " ")

	var result strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				result.WriteRune(' ')
				prevSpace = true
			}
		} else {
			result.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(result.String())
}

func highlightJSON(s string) string {
	if jsonLexer == nil || terminalFormatter == nil || jsonStyle == nil {
		return s
	}

	iterator, err := jsonLexer.Tokenise(nil, s)
	if err != nil {
		return s
	}

	var buf bytes.Buffer
	err = terminalFormatter.Format(&buf, jsonStyle, iterator)
	if err != nil {
		return s
	}

	result := strings.TrimSuffix(buf.String(), "\n")
	return result
}

func isANSITerminator(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}
