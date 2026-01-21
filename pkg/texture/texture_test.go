package texture

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestParseMetadata(t *testing.T) {
	// Create test metadata
	data := make([]byte, MetadataSize)
	binary.LittleEndian.PutUint32(data[0x00:], 512) // width
	binary.LittleEndian.PutUint32(data[0x04:], 512) // height
	binary.LittleEndian.PutUint32(data[0x08:], 10)  // mipLevels
	binary.LittleEndian.PutUint32(data[0x0C:], DXGI_FORMAT_BC7_UNORM)
	binary.LittleEndian.PutUint32(data[0x10:], 262288) // ddsFileSize
	binary.LittleEndian.PutUint32(data[0x14:], 262144) // rawFileSize
	binary.LittleEndian.PutUint32(data[0x18:], 0)      // flags
	binary.LittleEndian.PutUint32(data[0x1C:], 1)      // arraySize

	meta, err := ParseMetadata(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Failed to parse metadata: %v", err)
	}

	if meta.Width != 512 {
		t.Errorf("Expected width 512, got %d", meta.Width)
	}
	if meta.Height != 512 {
		t.Errorf("Expected height 512, got %d", meta.Height)
	}
	if meta.MipLevels != 10 {
		t.Errorf("Expected 10 mipLevels, got %d", meta.MipLevels)
	}
	if meta.DXGIFormat != DXGI_FORMAT_BC7_UNORM {
		t.Errorf("Expected format BC7_UNORM, got %d", meta.DXGIFormat)
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	original := &TextureMetadata{
		Width:       1024,
		Height:      1024,
		MipLevels:   11,
		DXGIFormat:  DXGI_FORMAT_BC3_UNORM,
		DDSFileSize: 699192,
		RawFileSize: 699048,
		Flags:       0,
		ArraySize:   1,
	}

	// Encode
	data := original.ToBytes()
	if len(data) != MetadataSize {
		t.Errorf("Expected %d bytes, got %d", MetadataSize, len(data))
	}

	// Decode
	parsed, err := ParseMetadata(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	// Compare
	if parsed.Width != original.Width {
		t.Errorf("Width mismatch: expected %d, got %d", original.Width, parsed.Width)
	}
	if parsed.Height != original.Height {
		t.Errorf("Height mismatch: expected %d, got %d", original.Height, parsed.Height)
	}
	if parsed.MipLevels != original.MipLevels {
		t.Errorf("MipLevels mismatch: expected %d, got %d", original.MipLevels, parsed.MipLevels)
	}
	if parsed.DXGIFormat != original.DXGIFormat {
		t.Errorf("DXGIFormat mismatch: expected %d, got %d", original.DXGIFormat, parsed.DXGIFormat)
	}
}

func TestFormatName(t *testing.T) {
	tests := []struct {
		format   uint32
		expected string
	}{
		{DXGI_FORMAT_BC1_UNORM, "BC1_UNORM"},
		{DXGI_FORMAT_BC3_UNORM, "BC3_UNORM"},
		{DXGI_FORMAT_BC7_UNORM, "BC7_UNORM"},
		{DXGI_FORMAT_BC7_UNORM_SRGB, "BC7_UNORM_SRGB"},
		{9999, "UNKNOWN(0x270f)"},
	}

	for _, tt := range tests {
		name := FormatName(tt.format)
		if name != tt.expected {
			t.Errorf("Format %d: expected %s, got %s", tt.format, tt.expected, name)
		}
	}
}

func TestConvertRawBCToDDS(t *testing.T) {
	meta := &TextureMetadata{
		Width:       512,
		Height:      512,
		MipLevels:   10,
		DXGIFormat:  DXGI_FORMAT_BC7_UNORM,
		DDSFileSize: 262288,
		RawFileSize: 262144,
		ArraySize:   1,
	}

	// Create fake raw BC data
	rawData := make([]byte, meta.RawFileSize)

	ddsData, err := ConvertRawBCToDDS(rawData, meta)
	if err != nil {
		t.Fatalf("Failed to convert: %v", err)
	}

	// Check DDS magic
	if len(ddsData) < 4 {
		t.Fatal("DDS data too short")
	}

	magic := binary.LittleEndian.Uint32(ddsData[0:4])
	if magic != DDS_MAGIC {
		t.Errorf("Expected DDS magic 0x%08X, got 0x%08X", DDS_MAGIC, magic)
	}

	// Check header size
	if len(ddsData) < 148 {
		t.Errorf("DDS data should be at least 148 bytes (header + DX10), got %d", len(ddsData))
	}

	// Verify width and height in header
	width := binary.LittleEndian.Uint32(ddsData[16:20])
	height := binary.LittleEndian.Uint32(ddsData[12:16])

	if width != meta.Width {
		t.Errorf("Width in header: expected %d, got %d", meta.Width, width)
	}
	if height != meta.Height {
		t.Errorf("Height in header: expected %d, got %d", meta.Height, height)
	}

	// Verify total size
	expectedSize := 148 + int(meta.RawFileSize) // header + DX10 + raw data
	if len(ddsData) != expectedSize {
		t.Errorf("Total size: expected %d, got %d", expectedSize, len(ddsData))
	}
}

func TestCalculateLinearSize(t *testing.T) {
	tests := []struct {
		width    uint32
		height   uint32
		format   uint32
		expected uint32
	}{
		// BC1: 8 bytes per block
		{512, 512, DXGI_FORMAT_BC1_UNORM, 128 * 128 * 8},
		// BC7: 16 bytes per block
		{512, 512, DXGI_FORMAT_BC7_UNORM, 128 * 128 * 16},
		// Non-multiple of 4 (rounds up)
		{513, 513, DXGI_FORMAT_BC7_UNORM, 129 * 129 * 16},
	}

	for _, tt := range tests {
		size := calculateLinearSize(tt.width, tt.height, tt.format)
		if size != tt.expected {
			t.Errorf("%dx%d format %d: expected %d, got %d",
				tt.width, tt.height, tt.format, tt.expected, size)
		}
	}
}

func TestConvertRawBCToDDS_ValidationError(t *testing.T) {
	meta := &TextureMetadata{
		RawFileSize: 1000,
	}

	// Create raw data with wrong size
	rawData := make([]byte, 500)

	_, err := ConvertRawBCToDDS(rawData, meta)
	if err == nil {
		t.Error("Expected error for size mismatch, got nil")
	}
}

func TestConvertRawBCToDDS_NilMetadata(t *testing.T) {
	rawData := make([]byte, 100)

	_, err := ConvertRawBCToDDS(rawData, nil)
	if err == nil {
		t.Error("Expected error for nil metadata, got nil")
	}
}
