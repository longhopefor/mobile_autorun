package httpapi

import "testing"

func TestDeviceTransportCompatibility(t *testing.T) {
	for value, want := range map[string]string{"": "ws", "ws": "ws", "usb_reverse": "usb_reverse"} {
		got, err := deviceTransport(value)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", value, got, err)
		}
	}
	if _, err := deviceTransport("shell"); err == nil {
		t.Fatal("unknown transport accepted")
	}
}
