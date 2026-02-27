package manifest

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/zstd"
)

const (
	// DefaultCompressionLevel is the compression level used for building packages.
	DefaultCompressionLevel = zstd.BestSpeed

	// MaxPackageSize is the maximum size of a single package file.
	MaxPackageSize = math.MaxInt32
)

// Builder constructs packages and manifests from a set of files.
type Builder struct {
	outputDir        string
	packageName      string
	compressionLevel int
}

// NewBuilder creates a new package builder.
func NewBuilder(outputDir, packageName string) *Builder {
	return &Builder{
		outputDir:        outputDir,
		packageName:      packageName,
		compressionLevel: DefaultCompressionLevel,
	}
}

// SetCompressionLevel sets the compression level for the builder.
func (b *Builder) SetCompressionLevel(level int) {
	b.compressionLevel = level
}

// Build creates a package and manifest from the given file groups.
func (b *Builder) Build(fileGroups [][]ScannedFile) (*Manifest, error) {
	totalFiles := 0
	for _, group := range fileGroups {
		totalFiles += len(group)
	}

	manifest := &Manifest{
		Header: Header{
			PackageCount: 1,
			FrameContents: Section{
				ElementSize: 32,
			},
			Metadata: Section{
				ElementSize: 40,
			},
			Frames: Section{
				ElementSize: 16,
			},
		},
		FrameContents: make([]FrameContent, 0, totalFiles),
		Metadata:      make([]FileMetadata, 0, totalFiles),
		Frames:        make([]Frame, 0),
	}

	packagesDir := filepath.Join(b.outputDir, "packages")
	if err := os.MkdirAll(packagesDir, 0755); err != nil {
		return nil, fmt.Errorf("create packages dir: %w", err)
	}

	var (
		currentFrame  bytes.Buffer
		currentOffset uint32
		frameIndex    uint32
	)

	for _, group := range fileGroups {
		if len(group) == 0 {
			continue
		}

		// Write previous frame if not empty
		if currentFrame.Len() > 0 {
			if err := b.writeFrame(manifest, &currentFrame, frameIndex); err != nil {
				return nil, err
			}
			frameIndex++
			currentFrame.Reset()
			currentOffset = 0
		}

		for _, file := range group {
			var data []byte
			var err error

			if file.Path != "" {
				data, err = os.ReadFile(file.Path)
			} else if file.SrcPackage != nil && file.SrcContent != nil {
				data, err = file.SrcPackage.ReadContent(file.SrcContent)
				if err != nil && strings.Contains(err.Error(), "too short") {
					fmt.Printf("Warning: skipping corrupted file %x/%x: %v\n", file.TypeSymbol, file.FileSymbol, err)
					data = []byte{}
					err = nil
				}
			} else {
				err = fmt.Errorf("no source for file %x/%x", file.TypeSymbol, file.FileSymbol)
			}

			if err != nil {
				return nil, fmt.Errorf("read file %x/%x: %w", file.TypeSymbol, file.FileSymbol, err)
			}

			if !file.SkipManifest {
				b.addFileToManifest(manifest, file, frameIndex, currentOffset)
			}

			currentFrame.Write(data)
			currentOffset += uint32(len(data))
		}

		b.incrementSection(&manifest.Header.FrameContents, len(group))
		b.incrementSection(&manifest.Header.Metadata, len(group))
	}

	// Write final frame
	if currentFrame.Len() > 0 {
		if err := b.writeFrame(manifest, &currentFrame, frameIndex); err != nil {
			return nil, err
		}
	}

	// Add package terminator frames
	b.addTerminatorFrames(manifest)

	return manifest, nil
}

func (b *Builder) addFileToManifest(manifest *Manifest, file ScannedFile, frameIndex, offset uint32) {
	alignment := uint32(1)

	manifest.FrameContents = append(manifest.FrameContents, FrameContent{
		TypeSymbol: file.TypeSymbol,
		FileSymbol: file.FileSymbol,
		FrameIndex: frameIndex,
		DataOffset: offset,
		Size:       file.Size,
		Alignment:  alignment,
	})

	manifest.Metadata = append(manifest.Metadata, FileMetadata{
		TypeSymbol: file.TypeSymbol,
		FileSymbol: file.FileSymbol,
	})
}

func (b *Builder) writeFrame(manifest *Manifest, data *bytes.Buffer, index uint32) error {
	compressed, err := zstd.CompressLevel(nil, data.Bytes(), b.compressionLevel)
	if err != nil {
		return fmt.Errorf("compress frame %d: %w", index, err)
	}
	return b.writeCompressedFrame(manifest, compressed, uint32(data.Len()))
}

func (b *Builder) writeCompressedFrame(manifest *Manifest, compressed []byte, uncompressedSize uint32) error {
	packageIndex := manifest.Header.PackageCount - 1
	packagePath := filepath.Join(b.outputDir, "packages", fmt.Sprintf("%s_%d", b.packageName, packageIndex))

	// Check if we need a new package file
	var offset uint32
	if len(manifest.Frames) > 0 {
		lastFrame := manifest.Frames[len(manifest.Frames)-1]
		offset = lastFrame.Offset + lastFrame.CompressedSize
	}

	maxSize := int64(MaxPackageSize)
	if int64(offset) >= maxSize || int64(offset)+int64(len(compressed)) > maxSize {
		manifest.Header.PackageCount++
		packageIndex++
		packagePath = filepath.Join(b.outputDir, "packages", fmt.Sprintf("%s_%d", b.packageName, packageIndex))
		offset = 0
	}

	f, err := os.OpenFile(packagePath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open package %d: %w", packageIndex, err)
	}
	defer f.Close()

	if _, err := f.Write(compressed); err != nil {
		return fmt.Errorf("write compressed data: %w", err)
	}

	manifest.Frames = append(manifest.Frames, Frame{
		PackageIndex:   packageIndex,
		Offset:         offset,
		CompressedSize: uint32(len(compressed)),
		Length:         uncompressedSize,
	})

	b.incrementSection(&manifest.Header.Frames, 1)
	return nil
}

func (b *Builder) addTerminatorFrames(manifest *Manifest) {
	packagesDir := filepath.Join(b.outputDir, "packages")

	for i := uint32(0); i < manifest.Header.PackageCount; i++ {
		packagePath := filepath.Join(packagesDir, fmt.Sprintf("%s_%d", b.packageName, i))
		info, err := os.Stat(packagePath)
		if err != nil {
			continue
		}

		manifest.Frames = append(manifest.Frames, Frame{
			PackageIndex: i,
			Offset:       uint32(info.Size()),
		})
		b.incrementSection(&manifest.Header.Frames, 1)
	}

	// Final terminator frame
	manifest.Frames = append(manifest.Frames, Frame{})
	b.incrementSection(&manifest.Header.Frames, 1)
}

func (b *Builder) incrementSection(s *Section, count int) {
	s.Count += uint64(count)
	s.ElementCount += uint64(count)
	s.Length += s.ElementSize * uint64(count)
}
