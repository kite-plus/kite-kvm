package vm

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

// hostnameRE matches an RFC 1123 hostname: one or more dot-separated labels,
// each 1-63 chars of letters/digits/hyphens, not starting/ending with a hyphen.
var hostnameRE = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// validateHostname enforces RFC 1123. This is a security boundary: the hostname
// is written into the cloud-init seed, so anything other than the safe charset
// is rejected to prevent cloud-config injection.
func validateHostname(h string) error {
	if len(h) == 0 || len(h) > 253 || !hostnameRE.MatchString(h) {
		return fmt.Errorf("%w: hostname must be a valid RFC 1123 hostname (letters, digits, hyphens, dots)", ErrInvalidRequest)
	}
	return nil
}

// validatePassword rejects control characters (including newlines) that could
// break out of the cloud-init document, and bounds the length.
func validatePassword(p string) error {
	if p == "" {
		return nil
	}
	if len(p) > 256 {
		return fmt.Errorf("%w: password is too long (max 256 bytes)", ErrInvalidRequest)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: password must not contain control characters", ErrInvalidRequest)
		}
	}
	return nil
}

// validateSSHKeys ensures every entry parses as an authorized_keys public key,
// which also guarantees it is a single safe line.
func validateSSHKeys(keys []string) error {
	for i, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			return fmt.Errorf("%w: ssh_keys[%d] is empty", ErrInvalidRequest, i)
		}
		// ParseAuthorizedKey parses only the first line; reject embedded newlines
		// so a multi-line value can never smuggle extra content.
		if strings.ContainsAny(k, "\n\r") {
			return fmt.Errorf("%w: ssh_keys[%d] must be a single line", ErrInvalidRequest, i)
		}
		if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k)); err != nil {
			return fmt.Errorf("%w: ssh_keys[%d] is not a valid SSH public key", ErrInvalidRequest, i)
		}
	}
	return nil
}
