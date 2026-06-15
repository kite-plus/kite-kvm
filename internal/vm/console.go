package vm

import (
	"context"

	"github.com/kite-plus/kite-kvm/internal/model"
)

// ConsoleEndpoint returns the VNC listen host and port of a running VM, for the
// API layer to mint a console token and proxy to it. The VM must be running for
// VNC to have an allocated port.
func (s *Service) ConsoleEndpoint(ctx context.Context, id string) (host string, port int, err error) {
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return "", 0, err
	}
	state, err := s.conn.DomainState(ctx, v.DomainName)
	if err != nil {
		return "", 0, err
	}
	if mapPowerState(state) != model.PowerRunning {
		return "", 0, ErrVMNotRunning
	}
	return s.conn.DomainVNCAddress(ctx, v.DomainName)
}
