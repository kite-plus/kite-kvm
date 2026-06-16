package vm

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestValidateHostname(t *testing.T) {
	good := []string{"web1", "web-1", "a", "host.example.com", "x123", "a-b-c.d-e"}
	for _, h := range good {
		if err := validateHostname(h); err != nil {
			t.Errorf("validateHostname(%q) = %v, want nil", h, err)
		}
	}
	bad := []string{"", "-bad", "bad-", "has space", "has\nnewline", "a..b", "..", strings.Repeat("a", 64), "rm\nruncmd: x"}
	for _, h := range bad {
		if err := validateHostname(h); err == nil {
			t.Errorf("validateHostname(%q) = nil, want error", h)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := validatePassword(""); err != nil {
		t.Errorf("empty password should be allowed: %v", err)
	}
	if err := validatePassword("S3cret-pass_!@#"); err != nil {
		t.Errorf("normal password rejected: %v", err)
	}
	for _, p := range []string{"has\nnewline", "tab\there", "bell\x07", strings.Repeat("x", 257)} {
		if err := validatePassword(p); err == nil {
			t.Errorf("validatePassword(%q) = nil, want error", p)
		}
	}
}

// testSSHKey is a freshly generated, valid ed25519 authorized_keys line, shared
// by tests that need a SSH key that passes validation.
var testSSHKey = func() string {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	sshPub, _ := ssh.NewPublicKey(pub)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}()

func TestValidateSSHKeys(t *testing.T) {
	validKey := testSSHKey

	if err := validateSSHKeys([]string{validKey}); err != nil {
		t.Errorf("valid key rejected: %v", err)
	}
	if err := validateSSHKeys(nil); err != nil {
		t.Errorf("no keys should be allowed: %v", err)
	}
	for _, k := range []string{"", "not-a-key", "ssh-rsa garbage", validKey + "\nruncmd: x"} {
		if err := validateSSHKeys([]string{k}); err == nil {
			t.Errorf("validateSSHKeys(%q) = nil, want error", k)
		}
	}
}
