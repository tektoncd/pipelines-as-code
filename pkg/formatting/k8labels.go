package formatting

import (
	"strings"
)

// CleanValueKubernetes conform a string to kubernetes naming convention
// see https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-subdomain-names
// rules are:
// • contain at most 63 characters
// • contain only lowercase alphanumeric characters or '-'
// • start with an alphanumeric character
// • end with an alphanumeric character.
func CleanValueKubernetes(s string) string {
	if len(s) >= 63 {
		// keep the last 62 characters
		s = s[len(s)-62:]
	}

	replasoeur := strings.NewReplacer(":", "-", "/", "-", " ", "_", "[", "__", "]", "__")
	s = strings.TrimRight(s, " .-_[]")
	s = strings.TrimLeft(s, " .-_[]")
	replaced := replasoeur.Replace(s)
	return strings.TrimSpace(replaced)
}
