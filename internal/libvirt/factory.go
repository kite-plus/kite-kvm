package libvirt

import "strings"

// New returns a Conn for the given libvirt URI. The sentinel "fake" / "fake://"
// (or an empty URI) returns the in-memory Fake — the right choice on a dev host
// without libvirt, such as macOS. Any other URI returns the go-libvirt-backed
// client that dials the local libvirtd socket.
func New(uri string) Conn {
	switch strings.TrimSpace(uri) {
	case "", "fake", "fake://":
		return NewFake()
	default:
		return newLibvirtConn(uri)
	}
}
