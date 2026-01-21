// Package asset provides parsers for Echo VR asset reference structures.
//
// Asset reference files (type 0xca6cd085401cbc87, 2,109 files) contain
// references between assets in various sizes (88-296 bytes).
// These structures link assets like materials, textures, and tints.
package asset

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ReferenceType indicates the type of asset reference.
type ReferenceType uint8

const (
	ReferenceTypeUnknown  ReferenceType = 0
	ReferenceTypeMaterial ReferenceType = 88
	ReferenceTypeTint     ReferenceType = 96
	ReferenceTypeTexture  ReferenceType = 120
	ReferenceTypeDual     ReferenceType = 136
	ReferenceTypeComplex  ReferenceType = 200
)

// AssetReference represents a generic asset reference structure.
type AssetReference struct {
	Size           uint32 // Size of the reference structure
	ReferenceGUID  uint64 // Asset being referenced
	TargetGUID     uint64 // Target asset (if applicable)
	Flags          uint32 // Reference flags
	Type           ReferenceType
	AdditionalData []byte // Additional type-specific data
}

// ParseReference reads an asset reference from binary data.
func ParseReference(r io.Reader) (*AssetReference, error) {
	// Read all data first to determine structure
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read reference data: %w", err)
	}

	size := uint32(len(data))
	if size < 8 {
		return nil, fmt.Errorf("data too short for asset reference: %d bytes", size)
	}

	// Parse based on size
	switch size {
	case 88:
		return parseMaterialReference(data)
	case 96:
		return parseTintReference(data)
	case 120:
		return parseTextureReference(data)
	case 136:
		return parseDualReference(data)
	case 200, 296:
		return parseComplexReference(data)
	default:
		return parseGenericReference(data)
	}
}

// parseMaterialReference parses an 88-byte material reference.
func parseMaterialReference(data []byte) (*AssetReference, error) {
	if len(data) < 88 {
		return nil, fmt.Errorf("data too short for material reference")
	}

	ref := &AssetReference{
		Size:          88,
		Type:          ReferenceTypeMaterial,
		ReferenceGUID: binary.LittleEndian.Uint64(data[0:8]),
		TargetGUID:    binary.LittleEndian.Uint64(data[8:16]),
		Flags:         binary.LittleEndian.Uint32(data[16:20]),
	}

	// Store remaining data
	ref.AdditionalData = make([]byte, len(data)-20)
	copy(ref.AdditionalData, data[20:])

	return ref, nil
}

// parseTintReference parses a 96-byte tint reference.
// Note: This is different from the tint data itself (also 96 bytes).
// Tint references link to tint assets, while tint data contains color information.
func parseTintReference(data []byte) (*AssetReference, error) {
	if len(data) < 96 {
		return nil, fmt.Errorf("data too short for tint reference")
	}

	ref := &AssetReference{
		Size:          96,
		Type:          ReferenceTypeTint,
		ReferenceGUID: binary.LittleEndian.Uint64(data[0:8]),
		TargetGUID:    binary.LittleEndian.Uint64(data[8:16]),
		Flags:         binary.LittleEndian.Uint32(data[16:20]),
	}

	ref.AdditionalData = make([]byte, len(data)-20)
	copy(ref.AdditionalData, data[20:])

	return ref, nil
}

// parseTextureReference parses a 120-byte texture reference.
func parseTextureReference(data []byte) (*AssetReference, error) {
	if len(data) < 120 {
		return nil, fmt.Errorf("data too short for texture reference")
	}

	ref := &AssetReference{
		Size:          120,
		Type:          ReferenceTypeTexture,
		ReferenceGUID: binary.LittleEndian.Uint64(data[0:8]),
		TargetGUID:    binary.LittleEndian.Uint64(data[8:16]),
		Flags:         binary.LittleEndian.Uint32(data[16:20]),
	}

	ref.AdditionalData = make([]byte, len(data)-20)
	copy(ref.AdditionalData, data[20:])

	return ref, nil
}

// parseDualReference parses a 136-byte dual reference.
func parseDualReference(data []byte) (*AssetReference, error) {
	if len(data) < 136 {
		return nil, fmt.Errorf("data too short for dual reference")
	}

	ref := &AssetReference{
		Size:          136,
		Type:          ReferenceTypeDual,
		ReferenceGUID: binary.LittleEndian.Uint64(data[0:8]),
		TargetGUID:    binary.LittleEndian.Uint64(data[8:16]),
		Flags:         binary.LittleEndian.Uint32(data[16:20]),
	}

	ref.AdditionalData = make([]byte, len(data)-20)
	copy(ref.AdditionalData, data[20:])

	return ref, nil
}

// parseComplexReference parses large references (200+ bytes).
func parseComplexReference(data []byte) (*AssetReference, error) {
	if len(data) < 20 {
		return nil, fmt.Errorf("data too short for complex reference")
	}

	ref := &AssetReference{
		Size:          uint32(len(data)),
		Type:          ReferenceTypeComplex,
		ReferenceGUID: binary.LittleEndian.Uint64(data[0:8]),
		TargetGUID:    binary.LittleEndian.Uint64(data[8:16]),
		Flags:         binary.LittleEndian.Uint32(data[16:20]),
	}

	ref.AdditionalData = make([]byte, len(data)-20)
	copy(ref.AdditionalData, data[20:])

	return ref, nil
}

// parseGenericReference parses unknown reference types.
func parseGenericReference(data []byte) (*AssetReference, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("data too short for generic reference")
	}

	ref := &AssetReference{
		Size:          uint32(len(data)),
		Type:          ReferenceTypeUnknown,
		ReferenceGUID: binary.LittleEndian.Uint64(data[0:8]),
	}

	if len(data) >= 16 {
		ref.TargetGUID = binary.LittleEndian.Uint64(data[8:16])
	}
	if len(data) >= 20 {
		ref.Flags = binary.LittleEndian.Uint32(data[16:20])
	}

	if len(data) > 20 {
		ref.AdditionalData = make([]byte, len(data)-20)
		copy(ref.AdditionalData, data[20:])
	}

	return ref, nil
}

// String returns a human-readable representation.
func (r *AssetReference) String() string {
	return fmt.Sprintf(
		"AssetRef[type=%s, size=%d, ref=%016x, target=%016x, flags=0x%x, extra=%d bytes]",
		r.Type, r.Size, r.ReferenceGUID, r.TargetGUID, r.Flags, len(r.AdditionalData),
	)
}

// String returns the reference type name.
func (t ReferenceType) String() string {
	switch t {
	case ReferenceTypeMaterial:
		return "Material"
	case ReferenceTypeTint:
		return "Tint"
	case ReferenceTypeTexture:
		return "Texture"
	case ReferenceTypeDual:
		return "Dual"
	case ReferenceTypeComplex:
		return "Complex"
	default:
		return "Unknown"
	}
}
