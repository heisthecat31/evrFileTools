package archive

import (
	"fmt"
	"io"
	"github.com/DataDog/zstd"
)

// fastDecodeAll reads all bytes from r, skips the archive header, and uses
// DecodeAll for bulk decompression. This is ~1000x faster than streaming
// for the game's manifest files (which use non-single-segment zstd frames).
func fastDecodeAll(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if len(raw) < HeaderSize {
		return nil, fmt.Errorf("file too short for archive header")
	}

	hdr := &Header{}
	if err := hdr.UnmarshalBinary(raw[:HeaderSize]); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}

	payloadEnd := uint64(HeaderSize) + hdr.CompressedLength
	if uint64(len(raw)) < payloadEnd {
		return nil, fmt.Errorf("file too short: need %d, have %d", payloadEnd, len(raw))
	}
	compressed := raw[HeaderSize:payloadEnd]

	data, err := zstd.Decompress(nil, compressed)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	return data, nil
}

const (
	// DefaultCompressionLevel is the default compression level for encoding (SpeedDefault).
	DefaultCompressionLevel = zstd.BestSpeed
)

// Reader wraps an io.ReadSeeker to provide decompression of archive data.
type Reader struct {
	header    *Header
	zReader   io.ReadCloser
	headerBuf [HeaderSize]byte // Reusable buffer for header decoding
}

// NewReader creates a new archive reader from the given source.
// It reads and validates the header, then returns a reader for the decompressed content.
func NewReader(r io.ReadSeeker) (*Reader, error) {
	reader := &Reader{
		header: &Header{},
	}

	if _, err := r.Read(reader.headerBuf[:]); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	if err := reader.header.UnmarshalBinary(reader.headerBuf[:]); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}

	reader.zReader = zstd.NewReader(r)
	return reader, nil
}

// Header returns the archive header.
func (r *Reader) Header() *Header {
	return r.header
}

// Read reads decompressed data into p.
func (r *Reader) Read(p []byte) (n int, err error) {
	return r.zReader.Read(p)
}

// Close closes the reader.
func (r *Reader) Close() error {
	r.zReader.Close()
	return nil
}

// Length returns the uncompressed data length.
func (r *Reader) Length() int {
	return int(r.header.Length)
}

// CompressedLength returns the compressed data length.
func (r *Reader) CompressedLength() int {
	return int(r.header.CompressedLength)
}

// ReadAll reads the entire decompressed content from an archive.
// Uses fastDecodeAll (bulk DecodeAll) to avoid hangs with the game's
// non-single-segment zstd frames.
func ReadAll(r io.ReadSeeker) ([]byte, error) {
	return fastDecodeAll(r)
}

// DecodeRaw decompresses raw archive bytes (already loaded into memory).
// Equivalent to ReadAll but takes a []byte instead of io.ReadSeeker,
// avoiding an extra file-read step when the caller already has the bytes.
func DecodeRaw(raw []byte) ([]byte, error) {
	if len(raw) < HeaderSize {
		return nil, fmt.Errorf("archive too short")
	}
	hdr := &Header{}
	if err := hdr.UnmarshalBinary(raw[:HeaderSize]); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	payloadEnd := uint64(HeaderSize) + hdr.CompressedLength
	if uint64(len(raw)) < payloadEnd {
		return nil, fmt.Errorf("archive truncated: need %d, have %d", payloadEnd, len(raw))
	}
	compressed := raw[HeaderSize:payloadEnd]

	data, err := zstd.Decompress(nil, compressed)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	return data, nil
}
