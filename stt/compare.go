package stt

import (
	"strconv"
	"strings"
)

// CommandsEquivalent checks if two command strings are equivalent,
// considering altitude-aware flexibility for A/D/C commands.
// For example, "A40" and "D40" are equivalent if the aircraft is above 4000 ft.
func CommandsEquivalent(expected, actual string, aircraft map[string]Aircraft) bool {
	if expected == actual {
		return true
	}

	// Split into callsign and commands
	expectedParts := strings.Fields(expected)
	actualParts := strings.Fields(actual)

	// The frequency-change grammar can emit FC/TO hints containing spaces
	// (e.g. "FC132400:Los Angeles Center"). Collapse trailing hint words into
	// the preceding FC/TO token so field-wise comparison still lines up with
	// legacy regression strings like "FC".
	actualParts = collapseFCTOHintParts(actualParts)
	expectedParts = collapseFCTOHintParts(expectedParts)

	if len(expectedParts) != len(actualParts) {
		return false
	}

	if len(expectedParts) == 0 {
		return true
	}

	// First part is callsign - must match exactly
	if expectedParts[0] != actualParts[0] {
		return false
	}

	callsign := expectedParts[0]

	// Find the aircraft for altitude context.
	// Strip /T suffix for lookup since aircraft map may have either form.
	rawCallsign := strings.TrimSuffix(callsign, "/T")
	var ac Aircraft
	var found bool
	for _, a := range aircraft {
		if strings.TrimSuffix(a.Callsign, "/T") == rawCallsign {
			ac = a
			found = true
			break
		}
	}

	// Compare each command
	for i := 1; i < len(expectedParts); i++ {
		if !commandEquivalent(expectedParts[i], actualParts[i], ac, found) {
			return false
		}
	}

	return true
}

// collapseFCTOHintParts merges trailing words into any FC{digits}:hint or
// TO{digits}:hint token. The STT grammar emits position hints like
// "FC132400:Los Angeles Center" which strings.Fields splits into multiple
// parts; this rejoins them so comparisons against legacy "FC" / "TO"
// expectations see a single command token.
func collapseFCTOHintParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		out = append(out, p)
		isFC := strings.HasPrefix(p, "FC") && len(p) > 2 && p[2] >= '0' && p[2] <= '9'
		isTO := strings.HasPrefix(p, "TO") && len(p) > 2 && p[2] >= '0' && p[2] <= '9'
		if !(isFC || isTO) {
			continue
		}
		if !strings.Contains(p, ":") {
			continue
		}
		// Absorb subsequent word-only parts (no digits, no ':' separator) that
		// are plausibly part of a multi-word position hint.
		for i+1 < len(parts) {
			next := parts[i+1]
			if isLikelyCommandToken(next) {
				break
			}
			out[len(out)-1] = out[len(out)-1] + " " + next
			i++
		}
	}
	return out
}

// isLikelyCommandToken reports whether s looks like a standalone command
// (e.g. "D50", "S170/U5", "DGANDY") rather than a continuation of a
// position hint. A token is considered a command if it starts with an
// uppercase letter followed by a digit, or contains '/', or is all-caps 5+.
func isLikelyCommandToken(s string) bool {
	if s == "" {
		return true
	}
	if strings.ContainsAny(s, "/") {
		return true
	}
	// Starts with capital then digit: C110, D50, S170, A120 …
	if len(s) >= 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= '0' && s[1] <= '9' {
		return true
	}
	// All-uppercase fix names (e.g. DGANDY, DKEWB). Hint words from the text
	// parser are title-cased (e.g. "Boston"), so all-caps implies command.
	allUpper := true
	hasLetter := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			allUpper = false
			break
		}
		if r >= 'A' && r <= 'Z' {
			hasLetter = true
		}
	}
	if hasLetter && allUpper {
		return true
	}
	return false
}

// commandEquivalent checks if two individual commands are equivalent.
func commandEquivalent(expected, actual string, ac Aircraft, hasAircraftContext bool) bool {
	if expected == actual {
		return true
	}

	// FC/TO commands: bare "FC"/"TO" in expected is equivalent to any
	// "FC{digits}[:hint]" / "TO{digits}[:hint]" in actual. The richer forms
	// were added by the frequency-change-readback grammar upgrade; legacy
	// regression expectations only capture the command type.
	for _, prefix := range []string{"FC", "TO"} {
		if expected == prefix && strings.HasPrefix(actual, prefix) && len(actual) > len(prefix) {
			rest := actual[len(prefix):]
			// rest must start with a digit (frequency) to be a match.
			if len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
				return true
			}
		}
	}

	// Check for A/D/C altitude command equivalence
	if hasAircraftContext && len(expected) > 1 && len(actual) > 1 {
		expType := expected[0]
		actType := actual[0]
		expAlt := expected[1:]
		actAlt := actual[1:]

		// Both must have the same altitude value
		if expAlt != actAlt {
			return false
		}

		// Check if they're altitude commands (A, D, or C followed by digits)
		if !IsNumber(expAlt) {
			return false
		}

		alt, err := strconv.Atoi(expAlt)
		if err != nil {
			return false
		}
		altFeet := alt * 100

		// A and D are equivalent if aircraft is above target altitude
		if (expType == 'A' && actType == 'D') || (expType == 'D' && actType == 'A') {
			if ac.Altitude > altFeet {
				return true
			}
		}

		// A and C are equivalent if aircraft is below target altitude
		if (expType == 'A' && actType == 'C') || (expType == 'C' && actType == 'A') {
			if ac.Altitude < altFeet {
				return true
			}
		}
	}

	return false
}
