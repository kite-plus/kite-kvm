// Package vm is the domain core: it translates API intents into ordered
// operations across the provisioner, network manager, libvirt client, and store,
// and owns the VM's lifecycle status and suspend semantics.
package vm

import "errors"

// Service-level sentinel errors. The API layer maps these to HTTP status codes.
var (
	ErrFlavorNotFound  = errors.New("flavor not found")
	ErrImageNotFound   = errors.New("image not found")
	ErrNetworkNotFound = errors.New("network not found")
	ErrInvalidRequest  = errors.New("invalid request")
	ErrVMNotFound      = errors.New("vm not found")
	ErrVMTerminated    = errors.New("vm is terminated")
)

// CreateRequest is the body of POST /v1/vms.
type CreateRequest struct {
	FlavorID string         `json:"flavor_id"`
	ImageID  string         `json:"image_id"`
	Hostname string         `json:"hostname,omitempty"`
	Password string         `json:"password,omitempty"`
	SSHKeys  []string       `json:"ssh_keys,omitempty"`
	Network  NetworkRequest `json:"network,omitempty"`
}

// NetworkRequest selects the network for a new VM. An explicit network_id wins;
// otherwise a mode (nat|bridge) picks that mode's default network; otherwise the
// configured default network is used.
type NetworkRequest struct {
	NetworkID string `json:"network_id,omitempty"`
	Mode      string `json:"mode,omitempty"`
}
