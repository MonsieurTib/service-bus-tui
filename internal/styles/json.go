package styles

import (
	"bytes"
	"encoding/json"
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

	if !json.Valid(body) {
		return truncateANSISafe(text, maxWidth)
	}
	highlighted := highlightJSON(text)

	return truncateANSISafe(highlighted, maxWidth)
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

func truncateANSISafe(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}

	var result strings.Builder
	visibleLen := 0
	runes := []rune(s)
	i := 0
	n := len(runes)

	effectiveMax := maxWidth - 3

	for i < n {
		if runes[i] == '\x1b' && i+1 < n && runes[i+1] == '[' {
			start := i
			i += 2
			for i < n && !isANSITerminator(runes[i]) {
				i++
			}
			if i < n {
				i++
			}
			result.WriteString(string(runes[start:i]))
			continue
		}

		if visibleLen >= effectiveMax {
			hasMore := hasMoreVisibleContent(runes, i)
			if hasMore {
				result.WriteString("...")
				result.WriteString("\x1b[0m")
				return result.String()
			}
		}

		if visibleLen >= maxWidth {
			result.WriteString("\x1b[0m")
			return result.String()
		}

		result.WriteRune(runes[i])
		visibleLen++
		i++
	}

	return result.String()
}

func isANSITerminator(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func hasMoreVisibleContent(runes []rune, i int) bool {
	for i < len(runes) {
		if runes[i] == '\x1b' {
			i++
			if i < len(runes) && runes[i] == '[' {
				i++
				for i < len(runes) && !isANSITerminator(runes[i]) {
					i++
				}
				if i < len(runes) {
					i++
				}
			}
			continue
		}
		return true
	}
	return false
}
