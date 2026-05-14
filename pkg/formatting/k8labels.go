package formatting

import (
	"slices"
	"strings"
	"unicode"

	"k8s.io/apimachinery/pkg/util/validation"
)

var (
	managedSpecialCharsMap = map[rune]string{
		':': "-",
		'/': "-",
		' ': "_",
		'[': "__",
		']': "__",
	}
	allowedSpecialCharsLabelValue      = ".-_"
	allowedSpecialCharsLabelValueRunes = []rune(allowedSpecialCharsLabelValue)

	// Build the replacer starting from the managedSpecialCharsMap.
	managedSpecialCharsInLabelValueReplacer = strings.NewReplacer(pairs(managedSpecialCharsMap)...)
)

// CleanValueKubernetes conforms a string to kubernetes naming convention
// see https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-subdomain-names
// rules are:
// • contain at most 63 characters
// • contain only alphanumeric characters or '-', '.', and '_'
// • start with an alphanumeric character
// • end with an alphanumeric character.
func CleanValueKubernetes(s string) string {
	// cut short if the string is already a valid label
	if len(validation.IsValidLabelValue(s)) == 0 {
		return s
	}

	// sanitize the input
	sanitized := sanitizeLabelValue(s)

	// replace the managed special symbols
	replaced := managedSpecialCharsInLabelValueReplacer.Replace(sanitized)

	// trim unwanted chars
	trimmed := strings.Trim(replaced, allowedSpecialCharsLabelValue)

	// cut to max length
	cut := cutToLabelValueMaxLength(trimmed)

	// trim left again to ensure no invalid values
	// are at the edge after the cut
	return strings.TrimLeft(cut, allowedSpecialCharsLabelValue)
}

// cutToLabelValueMaxLength cuts the provided string to the maximum length allowed
// for a kubernetes Label (63 chars).
func cutToLabelValueMaxLength(s string) string {
	if len(s) > validation.LabelValueMaxLength {
		return s[len(s)-(validation.LabelValueMaxLength):]
	}
	return s
}

// sanitizeLabelValue removes all unmanaged characters from the input string.
func sanitizeLabelValue(s string) string {
	b := strings.Builder{}
	for _, r := range s {
		if isSafe(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// pairs extracts all the key-value pairs from a rune-string map.
func pairs(m map[rune]string) []string {
	ss := make([]string, 0, len(m)*2)
	for k, v := range m {
		ss = append(ss, string(k), v)
	}
	return ss
}

// isSafe a helper to identify if a rune is safe to process or should be dropped.
func isSafe(r rune) bool {
	return !(unicode.IsSpace(r) && r != ' ') &&
		(isAllowedSpecialCharLabelValue(r) || isAlphanumeric(r) || isManagedSpecialChar(r))
}

// isAlphanumeric returns true if the rune is an alphanumeric value.
func isAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// isAllowedSpecialCharLabelValue returns true if the rune is an allowed char as per
// Kubernetes Label Values.
func isAllowedSpecialCharLabelValue(r rune) bool {
	return slices.Contains(allowedSpecialCharsLabelValueRunes, r)
}

// isManagedSpecialChar returns true if the rune is a managed special char.
// The normalization of the value will replace it with its counterpart.
func isManagedSpecialChar(r rune) bool {
	_, ok := managedSpecialCharsMap[r]
	return ok
}
