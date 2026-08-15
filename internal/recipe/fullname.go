package recipe

import (
	"errors"
	"strings"
)

// ValidateFullname enforces the portable GECOS field contract shared by
// recipe validation, the TUI, and both system-configuration writers. Empty
// values and Unicode are valid; ':' would split passwd fields and a line
// break would split the passwd record (or be rejected by useradd).
func ValidateFullname(fullname string) error {
	if strings.ContainsAny(fullname, ":\r\n") {
		return errors.New("must not contain ':' or line breaks")
	}
	return nil
}
