package libvirt

import "testing"

func TestNewSelectsFake(t *testing.T) {
	for _, uri := range []string{"", "fake", "fake://", "  fake://  "} {
		if _, ok := New(uri).(*Fake); !ok {
			t.Errorf("New(%q) should return *Fake", uri)
		}
	}
}

func TestNewSelectsLibvirt(t *testing.T) {
	c := New("qemu:///system")
	if _, ok := c.(*libvirtConn); !ok {
		t.Errorf("New(qemu:///system) should return *libvirtConn, got %T", c)
	}
}
