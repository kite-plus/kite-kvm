package domainxml

import (
	"strings"
	"testing"
)

func TestRenderInterfaceLinkDown(t *testing.T) {
	x, err := RenderInterface(Spec{
		MAC:      "52:54:00:aa:bb:cc",
		Network:  NetworkAttachment{Mode: ModeNAT, Source: "default"},
		LinkDown: true,
	})
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(x), "<interface") {
		t.Errorf("expected an <interface> element, got:\n%s", x)
	}
	if !strings.Contains(x, `<link state="down">`) && !strings.Contains(x, `<link state="down"/>`) {
		t.Errorf("expected link state down, got:\n%s", x)
	}
}

func TestRenderInterfaceLinkUp(t *testing.T) {
	x, err := RenderInterface(Spec{
		MAC:     "52:54:00:aa:bb:cc",
		Network: NetworkAttachment{Mode: ModeBridge, Source: "br0"},
	})
	if err != nil {
		t.Fatalf("RenderInterface: %v", err)
	}
	if strings.Contains(x, "link") {
		t.Errorf("link element should be omitted when up, got:\n%s", x)
	}
	if !strings.Contains(x, `bridge="br0"`) {
		t.Errorf("expected bridge source, got:\n%s", x)
	}
}

func TestRenderInterfaceValidation(t *testing.T) {
	if _, err := RenderInterface(Spec{Network: NetworkAttachment{Mode: ModeNAT, Source: "default"}}); err == nil {
		t.Error("expected error for missing mac")
	}
}

// TestFullDomainLinkDownBaked ensures LinkDown flows into the full domain render.
func TestFullDomainLinkDownBaked(t *testing.T) {
	x, err := Render(Spec{
		Name: "kvm-x", VCPUs: 1, MemoryMB: 512,
		DiskPath: "/d.qcow2", MAC: "52:54:00:aa:bb:cc",
		Network:  NetworkAttachment{Mode: ModeNAT, Source: "default"},
		LinkDown: true,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(x, `<link state="down"`) {
		t.Errorf("expected baked link down in full domain, got:\n%s", x)
	}
}
