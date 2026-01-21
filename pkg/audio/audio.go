// Package audio provides parsers for Echo VR audio reference structures.
//
// Audio reference files (type 0x38ee951a26fb816a, 119 files) contain
// indices or references to audio assets within the game's audio system.
package audio

import (
	"encoding/binary"
	"fmt"
	"io"
)

// AudioReference represents an audio asset reference structure.
// Based on analysis of 119 files, typical structure appears to be:
// - 8-byte GUID/type identifier (0x38ee951a26fb816a)
// - 8-byte asset reference
// - Additional metadata fields
type AudioReference struct {
	GUIDType       uint64 // +0x00: Type GUID (0x38ee951a26fb816a)
	AssetReference uint64 // +0x08: Reference to audio asset
	Count          uint32 // +0x10: Number of entries or flags
	Flags          uint32 // +0x14: Additional flags
	Reserved       []byte // Variable size remaining data
}

// ParseAudioReference reads an audio reference from binary data.
func ParseAudioReference(r io.Reader) (*AudioReference, error) {
	// Read at minimum 24 bytes for the basic structure
	buf := make([]byte, 24)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("failed to read audio reference header: %w", err)
	}

	ref := &AudioReference{
		GUIDType:       binary.LittleEndian.Uint64(buf[0x00:0x08]),
		AssetReference: binary.LittleEndian.Uint64(buf[0x08:0x10]),
		Count:          binary.LittleEndian.Uint32(buf[0x10:0x14]),
		Flags:          binary.LittleEndian.Uint32(buf[0x14:0x18]),
	}

	// Read any remaining data
	remaining, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read remaining data: %w", err)
	}
	ref.Reserved = remaining

	return ref, nil
}

// AudioIndex represents a collection of audio references.
type AudioIndex struct {
	References []AudioReference
}

// ParseAudioIndex reads multiple audio references from binary data.
// The format and count are determined by analyzing the data structure.
func ParseAudioIndex(r io.Reader) (*AudioIndex, error) {
	// Read all data first to determine structure
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read audio index: %w", err)
	}

	// Try to parse as repeated structures
	index := &AudioIndex{}

	// Basic structure appears to be fixed-size entries
	// Analyze the data to determine entry size
	if len(data) < 24 {
		return nil, fmt.Errorf("data too short for audio index")
	}

	// For now, treat entire file as a single reference
	// This may need adjustment based on actual file analysis
	ref, err := ParseAudioReference(io.NewSectionReader(
		&readerAt{data}, 0, int64(len(data)),
	))
	if err != nil {
		return nil, err
	}

	index.References = append(index.References, *ref)
	return index, nil
}

// String returns a human-readable representation.
func (r *AudioReference) String() string {
	return fmt.Sprintf(
		"AudioRef[guid=%016x, asset=%016x, count=%d, flags=0x%x, extra=%d bytes]",
		r.GUIDType, r.AssetReference, r.Count, r.Flags, len(r.Reserved),
	)
}

// readerAt adapts a byte slice to io.ReaderAt
type readerAt struct {
	data []byte
}

func (r *readerAt) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 || off > int64(len(r.data)) {
		return 0, io.EOF
	}
	n = copy(p, r.data[off:])
	if n < len(p) {
		err = io.EOF
	}
	return
}
