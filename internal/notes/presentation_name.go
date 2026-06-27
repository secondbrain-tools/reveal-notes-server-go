package notes

import (
	"fmt"
	"regexp"
)

// validPresentationName matches alphanumeric names with dots, underscores, hyphens.
// Must start with a letter or digit, max 255 characters.
var validPresentationName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,254}$`)

// ValidatePresentationName reports whether a presentation slug is valid.
func ValidatePresentationName(name string) error {
	if !validPresentationName.MatchString(name) {
		return fmt.Errorf("invalid presentation name: %q", name)
	}
	return nil
}
