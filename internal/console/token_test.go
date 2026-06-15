package console

import (
	"testing"
	"time"
)

func TestMintRedeemSingleUse(t *testing.T) {
	s := NewTokenStore(time.Minute)
	tok, ticket, err := s.Mint("vm1", "127.0.0.1", 5901)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if tok == "" || ticket.Port != 5901 {
		t.Fatalf("bad mint: %q %+v", tok, ticket)
	}

	got, ok := s.Redeem(tok)
	if !ok || got.VMID != "vm1" || got.Host != "127.0.0.1" || got.Port != 5901 {
		t.Fatalf("redeem = %+v ok=%v", got, ok)
	}
	// Single-use: a second redeem fails.
	if _, ok := s.Redeem(tok); ok {
		t.Error("token should be single-use")
	}
}

func TestRedeemUnknown(t *testing.T) {
	s := NewTokenStore(time.Minute)
	if _, ok := s.Redeem("nope"); ok {
		t.Error("unknown token should not redeem")
	}
}

func TestRedeemExpired(t *testing.T) {
	s := NewTokenStore(time.Millisecond)
	tok, _, _ := s.Mint("vm1", "127.0.0.1", 5901)
	time.Sleep(5 * time.Millisecond)
	if _, ok := s.Redeem(tok); ok {
		t.Error("expired token should not redeem")
	}
}
