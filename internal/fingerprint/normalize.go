// Package fingerprint reduces log lines to stable
package fingerprint

import "strings"

// maxNormalized caps the length of a normalized
const maxNormalized = 512

// Normalize reduces a raw log line to its stable
func Normalize(line string) string {
	// Progress-bar overwrite semantics: the text after
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		line = line[i+1:]
	}
	bufCap := len(line)
	if bufCap > maxNormalized {
		bufCap = maxNormalized
	}
	out := make([]byte, 0, bufCap)
	pendingSpace := false
	i := 0
	for i < len(line) && len(out) < maxNormalized {
		c := line[i]
		switch {
		case c == 0x1b:
			i = skipEscape(line, i)
		case isSpace(c):
			pendingSpace = true
			i++
		case isWordByte(c):
			out = emitSpace(out, &pendingSpace)
			if uuidAt(line, i) {
				out = append(out, '#')
				i += uuidLen
				continue
			}
			j := i + 1
			for j < len(line) && isWordByte(line[j]) {
				j++
			}
			out = appendToken(out, line[i:j])
			i = j
		default:
			out = emitSpace(out, &pendingSpace)
			out = append(out, c)
			i++
		}
	}
	if len(out) > maxNormalized {
		out = out[:maxNormalized]
	}
	return string(out)
}

// emitSpace flushes a pending collapsed space
func emitSpace(out []byte, pending *bool) []byte {
	if *pending {
		if len(out) > 0 {
			out = append(out, ' ')
		}
		*pending = false
	}
	return out
}

// skipEscape consumes an ANSI escape sequence
func skipEscape(s string, i int) int {
	i++ // consume ESC
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[': // CSI
		i++
		for i < len(s) {
			c := s[i]
			i++
			if c >= 0x40 && c <= 0x7e {
				break
			}
		}
		return i
	case ']': // OSC
		i++
		for i < len(s) {
			c := s[i]
			if c == 0x07 { // BEL terminator
				return i + 1
			}
			if c == 0x1b {
				if i+1 < len(s) && s[i+1] == '\\' { // ESC \ terminator
					return i + 2
				}
				return i // malformed: let the outer loop handle the new ESC
			}
			i++
		}
		return i
	default: // other sequence
		return i + 1
	}
}

// appendToken appends the normalized form of word
func appendToken(out []byte, tok string) []byte {
	allDigits, allHex, hasDigit, hasLetter := true, true, false, false
	for i := 0; i < len(tok); i++ {
		if isDigit(tok[i]) {
			hasDigit = true
			continue
		}
		allDigits = false
		hasLetter = true // tokens contain only
		if !isHexByte(tok[i]) {
			allHex = false
		}
	}
	switch {
	case allDigits:
		return append(out, '#')
	case len(tok) > 2 && tok[0] == '0' && (tok[1] == 'x' || tok[1] == 'X') && allHexStr(tok[2:]):
		return append(out, '#')
	case allHex && len(tok) >= 6 && hasDigit && hasLetter:
		return append(out, '#')
	case allHex && len(tok) >= 8 && hasDigit:
		return append(out, '#')
	}
	for i := 0; i < len(tok); {
		if isDigit(tok[i]) {
			out = append(out, '#')
			for i < len(tok) && isDigit(tok[i]) {
				i++
			}
			continue
		}
		out = append(out, tok[i])
		i++
	}
	return out
}

// uuidLen is the byte length of a canonical UUID
const uuidLen = 36

var uuidGroups = [5]int{8, 4, 4, 4, 12}

// uuidAt reports whether s[i:] starts with a
func uuidAt(s string, i int) bool {
	if i+uuidLen > len(s) {
		return false
	}
	p := i
	for gi, gl := range uuidGroups {
		if gi > 0 {
			if s[p] != '-' {
				return false
			}
			p++
		}
		for k := 0; k < gl; k++ {
			if !isHexByte(s[p]) {
				return false
			}
			p++
		}
	}
	return p >= len(s) || !isWordByte(s[p])
}

func allHexStr(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isHexByte(s[i]) {
			return false
		}
	}
	return true
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || isDigit(c)
}

func isHexByte(c byte) bool {
	return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
