// Package texture provides parsers and converters for Echo VR texture assets.
//
// Echo VR uses three types of texture files:
// 1. Standard DDS files with full headers (type 0xbeac1969cb7b8861)
// 2. Raw BC compressed texture data without headers (type 0x7f5bc1cf8ce51ffd)
// 3. 256-byte texture metadata files (type 0x2f6e61706a2c8f35)
//
// The metadata files describe texture properties and are used to reconstruct
// proper DDS headers for the raw BC texture data.
package texture

import (
	"encoding/binary"
	"fmt"
	"io"
)

// DXGI_FORMAT constants for common texture formats
const (
	DXGI_FORMAT_UNKNOWN             = 0
	DXGI_FORMAT_BC1_UNORM           = 71
	DXGI_FORMAT_BC1_UNORM_SRGB      = 72
	DXGI_FORMAT_BC2_UNORM           = 74
	DXGI_FORMAT_BC2_UNORM_SRGB      = 75
	DXGI_FORMAT_BC3_UNORM           = 77
	DXGI_FORMAT_BC3_UNORM_SRGB      = 78
	DXGI_FORMAT_BC4_UNORM           = 80
	DXGI_FORMAT_BC4_SNORM           = 81
	DXGI_FORMAT_BC5_UNORM           = 83
	DXGI_FORMAT_BC5_SNORM           = 84
	DXGI_FORMAT_BC6H_UF16           = 95
	DXGI_FORMAT_BC6H_SF16           = 96
	DXGI_FORMAT_BC7_UNORM           = 98
	DXGI_FORMAT_BC7_UNORM_SRGB      = 99
	DXGI_FORMAT_R8G8B8A8_UNORM      = 28
	DXGI_FORMAT_R8G8B8A8_UNORM_SRGB = 29
)

// TextureMetadata represents the 256-byte texture descriptor.
// Based on analysis of 12,275 metadata files, all exactly 256 bytes.
type TextureMetadata struct {
	Width       uint32    // +0x00: Texture width in pixels
	Height      uint32    // +0x04: Texture height in pixels
	MipLevels   uint32    // +0x08: Number of mipmap levels
	DXGIFormat  uint32    // +0x0C: DXGI_FORMAT enum value
	DDSFileSize uint32    // +0x10: Size of DDS file with header
	RawFileSize uint32    // +0x14: Size of raw BC data without header
	Flags       uint32    // +0x18: Texture flags
	ArraySize   uint32    // +0x1C: Array size (1 for normal textures)
	Reserved    [224]byte // +0x20: Reserved/padding to 256 bytes
}

// MetadataSize is the fixed size of texture metadata files.
const MetadataSize = 256

// ParseMetadata reads texture metadata from binary data.
func ParseMetadata(r io.Reader) (*TextureMetadata, error) {
	data := make([]byte, MetadataSize)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	meta := &TextureMetadata{
		Width:       binary.LittleEndian.Uint32(data[0x00:0x04]),
		Height:      binary.LittleEndian.Uint32(data[0x04:0x08]),
		MipLevels:   binary.LittleEndian.Uint32(data[0x08:0x0C]),
		DXGIFormat:  binary.LittleEndian.Uint32(data[0x0C:0x10]),
		DDSFileSize: binary.LittleEndian.Uint32(data[0x10:0x14]),
		RawFileSize: binary.LittleEndian.Uint32(data[0x14:0x18]),
		Flags:       binary.LittleEndian.Uint32(data[0x18:0x1C]),
		ArraySize:   binary.LittleEndian.Uint32(data[0x1C:0x20]),
	}
	copy(meta.Reserved[:], data[0x20:])

	return meta, nil
}

// ToBytes serializes metadata to 256 bytes.
func (m *TextureMetadata) ToBytes() []byte {
	data := make([]byte, MetadataSize)
	binary.LittleEndian.PutUint32(data[0x00:0x04], m.Width)
	binary.LittleEndian.PutUint32(data[0x04:0x08], m.Height)
	binary.LittleEndian.PutUint32(data[0x08:0x0C], m.MipLevels)
	binary.LittleEndian.PutUint32(data[0x0C:0x10], m.DXGIFormat)
	binary.LittleEndian.PutUint32(data[0x10:0x14], m.DDSFileSize)
	binary.LittleEndian.PutUint32(data[0x14:0x18], m.RawFileSize)
	binary.LittleEndian.PutUint32(data[0x18:0x1C], m.Flags)
	binary.LittleEndian.PutUint32(data[0x1C:0x20], m.ArraySize)
	copy(data[0x20:], m.Reserved[:])
	return data
}

// String returns a human-readable representation.
func (m *TextureMetadata) String() string {
	return fmt.Sprintf(
		"Texture: %dx%d, %d mips, format=%s, dds_size=%d, raw_size=%d",
		m.Width, m.Height, m.MipLevels,
		FormatName(m.DXGIFormat),
		m.DDSFileSize, m.RawFileSize,
	)
}

// FormatName returns a human-readable name for a DXGI_FORMAT value.
func FormatName(format uint32) string {
	switch format {
	case DXGI_FORMAT_BC1_UNORM:
		return "BC1_UNORM"
	case DXGI_FORMAT_BC1_UNORM_SRGB:
		return "BC1_UNORM_SRGB"
	case DXGI_FORMAT_BC2_UNORM:
		return "BC2_UNORM"
	case DXGI_FORMAT_BC2_UNORM_SRGB:
		return "BC2_UNORM_SRGB"
	case DXGI_FORMAT_BC3_UNORM:
		return "BC3_UNORM"
	case DXGI_FORMAT_BC3_UNORM_SRGB:
		return "BC3_UNORM_SRGB"
	case DXGI_FORMAT_BC4_UNORM:
		return "BC4_UNORM"
	case DXGI_FORMAT_BC4_SNORM:
		return "BC4_SNORM"
	case DXGI_FORMAT_BC5_UNORM:
		return "BC5_UNORM"
	case DXGI_FORMAT_BC5_SNORM:
		return "BC5_SNORM"
	case DXGI_FORMAT_BC6H_UF16:
		return "BC6H_UF16"
	case DXGI_FORMAT_BC6H_SF16:
		return "BC6H_SF16"
	case DXGI_FORMAT_BC7_UNORM:
		return "BC7_UNORM"
	case DXGI_FORMAT_BC7_UNORM_SRGB:
		return "BC7_UNORM_SRGB"
	case DXGI_FORMAT_R8G8B8A8_UNORM:
		return "R8G8B8A8_UNORM"
	case DXGI_FORMAT_R8G8B8A8_UNORM_SRGB:
		return "R8G8B8A8_UNORM_SRGB"
	default:
		return fmt.Sprintf("UNKNOWN(0x%x)", format)
	}
}

// DDS header constants
const (
	DDS_MAGIC                    = 0x20534444 // "DDS "
	DDS_HEADER_SIZE              = 124
	DDS_HEADER_FLAGS_CAPS        = 0x1
	DDS_HEADER_FLAGS_HEIGHT      = 0x2
	DDS_HEADER_FLAGS_WIDTH       = 0x4
	DDS_HEADER_FLAGS_PITCH       = 0x8
	DDS_HEADER_FLAGS_PIXELFORMAT = 0x1000
	DDS_HEADER_FLAGS_MIPMAPCOUNT = 0x20000
	DDS_HEADER_FLAGS_LINEARSIZE  = 0x80000
	DDS_HEADER_FLAGS_DEPTH       = 0x800000

	DDS_SURFACE_FLAGS_TEXTURE = 0x1000
	DDS_SURFACE_FLAGS_MIPMAP  = 0x400000
	DDS_SURFACE_FLAGS_CUBEMAP = 0x200

	DDS_PIXELFORMAT_SIZE = 32
	DDS_FOURCC           = 0x4

	DX10_FOURCC = 0x30315844 // "DX10"
)

// ConvertRawBCToDDS converts headerless BC texture to DDS with proper header.
func ConvertRawBCToDDS(rawData []byte, meta *TextureMetadata) ([]byte, error) {
	if meta == nil {
		return nil, fmt.Errorf("metadata is required")
	}

	// Validate raw data size matches metadata
	if uint32(len(rawData)) != meta.RawFileSize {
		return nil, fmt.Errorf("raw data size %d doesn't match metadata size %d", len(rawData), meta.RawFileSize)
	}

	// Create DDS header
	header := createDDSHeader(meta)

	// Combine header and raw data
	ddsData := make([]byte, len(header)+len(rawData))
	copy(ddsData, header)
	copy(ddsData[len(header):], rawData)

	return ddsData, nil
}

// createDDSHeader creates a complete DDS header with DX10 extension.
func createDDSHeader(meta *TextureMetadata) []byte {
	// DDS file = 4 bytes magic + 124 bytes header + 20 bytes DX10 extension + data
	header := make([]byte, 4+DDS_HEADER_SIZE+20)

	// Magic "DDS "
	binary.LittleEndian.PutUint32(header[0:4], DDS_MAGIC)

	// DDS_HEADER starts at offset 4
	offset := 4

	// dwSize
	binary.LittleEndian.PutUint32(header[offset:offset+4], DDS_HEADER_SIZE)
	offset += 4

	// dwFlags
	flags := uint32(DDS_HEADER_FLAGS_CAPS | DDS_HEADER_FLAGS_HEIGHT | DDS_HEADER_FLAGS_WIDTH |
		DDS_HEADER_FLAGS_PIXELFORMAT | DDS_HEADER_FLAGS_LINEARSIZE)
	if meta.MipLevels > 1 {
		flags |= DDS_HEADER_FLAGS_MIPMAPCOUNT
	}
	binary.LittleEndian.PutUint32(header[offset:offset+4], flags)
	offset += 4

	// dwHeight
	binary.LittleEndian.PutUint32(header[offset:offset+4], meta.Height)
	offset += 4

	// dwWidth
	binary.LittleEndian.PutUint32(header[offset:offset+4], meta.Width)
	offset += 4

	// dwPitchOrLinearSize (linear size for compressed formats)
	linearSize := calculateLinearSize(meta.Width, meta.Height, meta.DXGIFormat)
	binary.LittleEndian.PutUint32(header[offset:offset+4], linearSize)
	offset += 4

	// dwDepth (unused)
	binary.LittleEndian.PutUint32(header[offset:offset+4], 0)
	offset += 4

	// dwMipMapCount
	binary.LittleEndian.PutUint32(header[offset:offset+4], meta.MipLevels)
	offset += 4

	// dwReserved1[11] (44 bytes)
	offset += 44

	// DDS_PIXELFORMAT (32 bytes)
	// dwSize
	binary.LittleEndian.PutUint32(header[offset:offset+4], DDS_PIXELFORMAT_SIZE)
	offset += 4

	// dwFlags (DDPF_FOURCC for DX10 extension)
	binary.LittleEndian.PutUint32(header[offset:offset+4], DDS_FOURCC)
	offset += 4

	// dwFourCC = "DX10"
	binary.LittleEndian.PutUint32(header[offset:offset+4], DX10_FOURCC)
	offset += 4

	// dwRGBBitCount, dwRBitMask, dwGBitMask, dwBBitMask, dwABitMask (20 bytes, all zero for DX10)
	offset += 20

	// dwCaps
	caps := uint32(DDS_SURFACE_FLAGS_TEXTURE)
	if meta.MipLevels > 1 {
		caps |= DDS_SURFACE_FLAGS_MIPMAP
	}
	binary.LittleEndian.PutUint32(header[offset:offset+4], caps)
	offset += 4

	// dwCaps2, dwCaps3, dwCaps4 (12 bytes)
	offset += 12

	// dwReserved2
	offset += 4

	// DX10 extension (20 bytes)
	// dxgiFormat
	binary.LittleEndian.PutUint32(header[offset:offset+4], meta.DXGIFormat)
	offset += 4

	// resourceDimension (3 = TEXTURE2D)
	binary.LittleEndian.PutUint32(header[offset:offset+4], 3)
	offset += 4

	// miscFlag
	binary.LittleEndian.PutUint32(header[offset:offset+4], 0)
	offset += 4

	// arraySize
	binary.LittleEndian.PutUint32(header[offset:offset+4], meta.ArraySize)
	offset += 4

	// miscFlags2
	binary.LittleEndian.PutUint32(header[offset:offset+4], 0)

	return header
}

// calculateLinearSize calculates the linear size for a compressed texture.
func calculateLinearSize(width, height, format uint32) uint32 {
	blockSize := uint32(16)

	// BC1 and BC4 use 8 bytes per block
	if format == DXGI_FORMAT_BC1_UNORM || format == DXGI_FORMAT_BC1_UNORM_SRGB ||
		format == DXGI_FORMAT_BC4_UNORM || format == DXGI_FORMAT_BC4_SNORM {
		blockSize = 8
	}

	// Calculate number of blocks (round up)
	blocksWide := (width + 3) / 4
	blocksHigh := (height + 3) / 4

	return blocksWide * blocksHigh * blockSize
}
