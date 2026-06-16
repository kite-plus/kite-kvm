package provision

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCloudInitInjectionBlocked proves that hostile values (newlines crafted to
// inject cloud-config keys) cannot break out of their YAML scalars.
func TestCloudInitInjectionBlocked(t *testing.T) {
	evilHost := "h\nruncmd:\n  - touch /tmp/pwned"
	evilPass := "p\nchpasswd: {expire: false}\nruncmd: [rm -rf /]"
	ci := CloudInit{
		InstanceID:  "vm-1",
		Hostname:    evilHost,
		DefaultUser: "ubuntu",
		Password:    evilPass,
	}

	var meta map[string]any
	if err := yaml.Unmarshal([]byte(ci.metaData()), &meta); err != nil {
		t.Fatalf("meta-data is not valid YAML: %v", err)
	}
	if _, ok := meta["runcmd"]; ok {
		t.Fatal("INJECTION: runcmd leaked into meta-data")
	}
	if meta["local-hostname"] != evilHost {
		t.Errorf("hostname not preserved as a scalar: %q", meta["local-hostname"])
	}

	var ud map[string]any
	if err := yaml.Unmarshal([]byte(ci.userData()), &ud); err != nil {
		t.Fatalf("user-data is not valid YAML: %v", err)
	}
	if _, ok := ud["runcmd"]; ok {
		t.Fatal("INJECTION: runcmd leaked into user-data")
	}
	if ud["hostname"] != evilHost {
		t.Errorf("hostname not preserved as a scalar: %q", ud["hostname"])
	}
	// chpasswd must remain the single intended mapping, not be restructured.
	if _, ok := ud["chpasswd"].(map[string]any); !ok {
		t.Errorf("chpasswd was restructured: %#v", ud["chpasswd"])
	}
}
