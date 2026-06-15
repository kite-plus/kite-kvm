package libvirt

import "testing"

func TestParseVNCFromXML(t *testing.T) {
	const running = `<domain><devices>
		<graphics type='vnc' port='5903' autoport='yes' listen='127.0.0.1'/>
	</devices></domain>`
	host, port, err := parseVNCFromXML(running)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if host != "127.0.0.1" || port != 5903 {
		t.Errorf("got %s:%d, want 127.0.0.1:5903", host, port)
	}
}

func TestParseVNCListenElement(t *testing.T) {
	const xml = `<domain><devices>
		<graphics type='vnc' port='5905' autoport='yes'>
			<listen type='address' address='10.0.0.5'/>
		</graphics>
	</devices></domain>`
	host, port, err := parseVNCFromXML(xml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if host != "10.0.0.5" || port != 5905 {
		t.Errorf("got %s:%d, want 10.0.0.5:5905", host, port)
	}
}

func TestParseVNCNotAllocated(t *testing.T) {
	const notRunning = `<domain><devices>
		<graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'/>
	</devices></domain>`
	if _, _, err := parseVNCFromXML(notRunning); err == nil {
		t.Error("expected error for unallocated port")
	}
}

func TestParseVNCNoGraphics(t *testing.T) {
	if _, _, err := parseVNCFromXML(`<domain><devices></devices></domain>`); err == nil {
		t.Error("expected error when no vnc device")
	}
}
