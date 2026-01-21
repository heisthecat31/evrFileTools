package tint

import (
	"bytes"
	"testing"
)

func TestColorFromBytes(t *testing.T) {
	// Test case: white color
	data := make([]byte, 16)
	// R=1.0, G=1.0, B=1.0, A=1.0 as float32 little-endian
	// 1.0 in IEEE 754 = 0x3F800000
	data[0], data[1], data[2], data[3] = 0x00, 0x00, 0x80, 0x3F     // R = 1.0
	data[4], data[5], data[6], data[7] = 0x00, 0x00, 0x80, 0x3F     // G = 1.0
	data[8], data[9], data[10], data[11] = 0x00, 0x00, 0x80, 0x3F   // B = 1.0
	data[12], data[13], data[14], data[15] = 0x00, 0x00, 0x80, 0x3F // A = 1.0

	color := ColorFromBytes(data)

	if color.R != 1.0 || color.G != 1.0 || color.B != 1.0 || color.A != 1.0 {
		t.Errorf("Expected RGBA(1.0, 1.0, 1.0, 1.0), got %v", color)
	}
}

func TestColorToBytes(t *testing.T) {
	color := Color{R: 1.0, G: 0.5, B: 0.0, A: 1.0}
	data := color.ToBytes()

	if len(data) != 16 {
		t.Errorf("Expected 16 bytes, got %d", len(data))
	}

	// Parse back and compare
	parsed := ColorFromBytes(data)
	if parsed.R != color.R || parsed.G != color.G || parsed.B != color.B || parsed.A != color.A {
		t.Errorf("Round-trip failed: original=%v, parsed=%v", color, parsed)
	}
}

func TestColorHex(t *testing.T) {
	tests := []struct {
		color    Color
		expected string
	}{
		{Color{1.0, 1.0, 1.0, 1.0}, "#FFFFFFFF"},
		{Color{0.0, 0.0, 0.0, 1.0}, "#000000FF"},
		{Color{1.0, 0.0, 0.0, 1.0}, "#FF0000FF"},
		{Color{0.5, 0.5, 0.5, 0.5}, "#7F7F7F7F"},
	}

	for _, tt := range tests {
		hex := tt.color.Hex()
		if hex != tt.expected {
			t.Errorf("Color %v: expected %s, got %s", tt.color, tt.expected, hex)
		}
	}
}

func TestColorCSS(t *testing.T) {
	color := Color{R: 1.0, G: 0.5, B: 0.25, A: 0.8}
	css := color.CSS()

	expected := "rgba(255, 127, 63, 0.800)"
	if css != expected {
		t.Errorf("Expected %s, got %s", expected, css)
	}
}

func TestTintEntryFromBytes(t *testing.T) {
	data := make([]byte, TintEntrySize)

	// Set ResourceID
	resourceID := uint64(0x74d228d09dc5dc86)
	for i := 0; i < 8; i++ {
		data[i] = byte(resourceID >> (i * 8))
	}

	// Set first color to white
	whiteColor := Color{1.0, 1.0, 1.0, 1.0}.ToBytes()
	copy(data[0x08:0x18], whiteColor)

	entry := TintEntryFromBytes(data)

	if entry == nil {
		t.Fatal("Failed to parse tint entry")
	}

	if entry.ResourceID != resourceID {
		t.Errorf("Expected ResourceID 0x%016x, got 0x%016x", resourceID, entry.ResourceID)
	}

	if entry.Colors[0].R != 1.0 {
		t.Errorf("Expected first color R=1.0, got %f", entry.Colors[0].R)
	}
}

func TestTintEntryRoundTrip(t *testing.T) {
	original := &TintEntry{
		ResourceID: 0x74d228d09dc5dc86,
		Colors: [5]Color{
			{1.0, 0.0, 0.0, 1.0},
			{0.0, 1.0, 0.0, 1.0},
			{0.0, 0.0, 1.0, 1.0},
			{1.0, 1.0, 0.0, 1.0},
			{1.0, 0.0, 1.0, 1.0},
		},
	}

	// Encode
	data := original.ToBytes()
	if len(data) != TintEntrySize {
		t.Errorf("Expected %d bytes, got %d", TintEntrySize, len(data))
	}

	// Decode
	parsed := TintEntryFromBytes(data)
	if parsed == nil {
		t.Fatal("Failed to parse encoded tint entry")
	}

	// Compare
	if parsed.ResourceID != original.ResourceID {
		t.Errorf("ResourceID mismatch: expected 0x%016x, got 0x%016x",
			original.ResourceID, parsed.ResourceID)
	}

	for i := 0; i < 5; i++ {
		if parsed.Colors[i] != original.Colors[i] {
			t.Errorf("Color %d mismatch: expected %v, got %v",
				i, original.Colors[i], parsed.Colors[i])
		}
	}
}

func TestTintEntryToCSS(t *testing.T) {
	entry := &TintEntry{
		ResourceID: 0x74d228d09dc5dc86,
		Colors: [5]Color{
			{1.0, 0.0, 0.0, 1.0},
			{0.0, 1.0, 0.0, 1.0},
			{0.0, 0.0, 1.0, 1.0},
			{1.0, 1.0, 0.0, 1.0},
			{1.0, 0.0, 1.0, 1.0},
		},
	}

	css := entry.ToCSS("rwd_tint_0000")

	if !bytes.Contains([]byte(css), []byte(":root {")) {
		t.Error("CSS output missing :root selector")
	}

	if !bytes.Contains([]byte(css), []byte("--tint-rwd-tint-0000-main-1:")) {
		t.Error("CSS output missing expected variable name")
	}

	if !bytes.Contains([]byte(css), []byte("rgba(255, 0, 0, 1.000)")) {
		t.Error("CSS output missing expected color value")
	}
}

func TestLookupTintName(t *testing.T) {
	tests := []struct {
		symbol   uint64
		expected string
	}{
		{0x74d228d09dc5dc86, "rwd_tint_0000"},
		{0x74d228d09dc5dc87, "rwd_tint_0001"},
		{0x3e474b60a9416aca, "rwd_tint_s1_a_default"},
		{0x0000000000000000, ""}, // Unknown
	}

	for _, tt := range tests {
		name := LookupTintName(tt.symbol)
		if name != tt.expected {
			t.Errorf("Symbol 0x%016x: expected %q, got %q", tt.symbol, tt.expected, name)
		}
	}
}
