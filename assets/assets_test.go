package assets

import "testing"

func TestLogoPNGIsValidPNG(t *testing.T) {
	png := LogoPNG()
	if len(png) < 8 {
		t.Fatalf("logo bytes too short: %d", len(png))
	}
	// PNG signature: 89 50 4E 47 0D 0A 1A 0A
	want := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	for i, w := range want {
		if png[i] != w {
			t.Fatalf("logo missing PNG signature at byte %d: got %x want %x", i, png[i], w)
		}
	}
}

func TestLogoPNGReturnsEmbeddedSlice(t *testing.T) {
	// Repeated calls must return the same underlying slice (the
	// embed is read-only and shared across calls).
	if &LogoPNG()[0] != &logoPNG[0] {
		t.Fatalf("LogoPNG does not return the embedded slice")
	}
}
