# evrFileTools

A Go library and CLI tool for working with EVR (Echo VR) package and manifest files.

> Thanks to [Exhibitmark](https://github.com/Exhibitmark) for [carnation](https://github.com/Exhibitmark/carnation) which helped with reversing the manifest format!

## Features

- Extract files from EVR packages
- Build new packages from extracted files
- Read and write EVR manifest files
- **Full texture conversion pipeline**: DDS ↔ PNG with BC1/BC3 compression
  - Decode: DDS → PNG (lossless storage for editing)
  - Encode: PNG → DDS with automatic format detection and mipmap generation
  - Supports BC1 (DXT1), BC3 (DXT5), BC5 (partial)
- Parse texture metadata and convert raw BC textures to DDS
- Display and export tint color data as CSS
- Parse audio and asset reference structures
- ZSTD compression/decompression with optimized context reuse

## Installation

```bash
go install github.com/EchoTools/evrFileTools/cmd/evrtools@latest
go install github.com/EchoTools/evrFileTools/cmd/showtints@latest
go install github.com/EchoTools/evrFileTools/cmd/texconv@latest
```

Or build from source:

```bash
git clone https://github.com/EchoTools/evrFileTools.git
cd evrFileTools
make build
```

**Note**: `texconv` requires `libsquish` for BC compression. On Arch Linux:
```bash
sudo pacman -S libsquish
```

## Usage

### evrtools - Package Management

#### Extract files from a package

```bash
evrtools -mode extract \
    -data ./ready-at-dawn-echo-arena/_data/5932408047/rad15/win10 \
    -package 48037dc70b0ecab2 \
    -output ./extracted
```

This extracts all files from the package. Output structure:
- `./output/<typeSymbol>/<fileSymbol>`

With `-preserve-groups`, frames are preserved:
- `./output/<frameIndex>/<typeSymbol>/<fileSymbol>`

### Build a package from files

```bash
evrtools -mode build \
    -input ./files \
    -output ./output \
    -package mypackage
```

Expected input structure: `./input/<frameIndex>/<typeSymbol>/<fileSymbol>`

### showtints - Tint Color Display

Display tint color data from extracted files:

```bash
# Show all tints with details
showtints ./extracted

# Export tints as CSS custom properties
showtints --css --known ./extracted > tints.css

# Show summary only
showtints --summary ./extracted

# Filter options
showtints --known --nonzero ./extracted
```

### texconv - Texture Converter

Convert between DDS (BC-compressed) and PNG (lossless) formats:

```bash
# Decode DDS to PNG for editing
texconv decode texture.dds texture.png

# Encode PNG back to DDS with automatic format detection
texconv encode texture.png texture.dds

# Show texture information
texconv info texture.dds

# Batch convert directory
texconv batch decode _extracted/ png_output/
texconv batch encode png_input/ dds_output/
```

**Features**:
- **BC1 (DXT1)**: RGB + 1-bit alpha, 4 bits/pixel, ~60% of EchoVR textures
- **BC3 (DXT5)**: RGBA with 8-bit alpha, 8 bits/pixel, ~20% of EchoVR textures
- **Automatic format detection**: Analyzes alpha usage to choose optimal format
- **Mipmap generation**: Box filter downsampling for complete mip chains
- **Round-trip tested**: PNG → DDS → PNG preserves visual quality

### CLI Options

### CLI Options

#### evrtools

| Flag | Description |
|------|-------------|
| `-mode` | Operation mode: `extract` or `build` |
| `-data` | Path to _data directory containing manifests/packages |
| `-package` | Package name (e.g., `48037dc70b0ecab2`) |
| `-input` | Input directory for build mode |
| `-output` | Output directory |
| `-preserve-groups` | Preserve frame grouping in extract output |
| `-force` | Allow non-empty output directory |

#### showtints

| Flag | Description |
|------|-------------|
| `-css` | Output tints as CSS custom properties |
| `-known` | Only show entries matching known tint hashes |
| `-nonzero` | Only show entries with non-zero color data |
| `-summary` | Only show summary statistics |
| `-raw` | Show raw hex bytes (default: true) |

#### texconv

| Command | Description |
|---------|-------------|
| `decode <in.dds> <out.png>` | Decompress DDS to PNG |
| `encode <in.png> <out.dds>` | Compress PNG to DDS |
| `info <file.dds>` | Display texture information |
| `batch <mode> <indir> <outdir>` | Batch convert directory |

**Supported formats**: BC1 (DXT1), BC3 (DXT5), BC5 (partial), BC6H/BC7 (decode only)

## Library Usage

### Package Extraction

```go
package main

import (
    "log"
    "github.com/EchoTools/evrFileTools/pkg/manifest"
)

func main() {
    // Read a manifest
    m, err := manifest.ReadFile("/path/to/manifests/packagename")
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Manifest: %d files in %d packages", m.FileCount(), m.PackageCount())

    // Open the package files
    pkg, err := manifest.OpenPackage(m, "/path/to/packages/packagename")
    if err != nil {
        log.Fatal(err)
    }
    defer pkg.Close()

    // Extract all files
    if err := pkg.Extract("./output"); err != nil {
        log.Fatal(err)
    }
}
```

### Texture Processing

```go
import (
    "github.com/EchoTools/evrFileTools/pkg/texture"
    "io/ioutil"
)

// Parse texture metadata
metaData, _ := ioutil.ReadFile("texture.meta")
meta, _ := texture.ParseMetadata(bytes.NewReader(metaData))

// Convert raw BC texture to DDS
rawData, _ := ioutil.ReadFile("texture.raw")
ddsData, _ := texture.ConvertRawBCToDDS(rawData, meta)

// Write DDS file
ioutil.WriteFile("texture.dds", ddsData, 0644)
```

### Tint Processing

```go
import (
    "github.com/EchoTools/evrFileTools/pkg/tint"
    "os"
)

// Read tint entry
file, _ := os.Open("tint_file")
defer file.Close()

entry, _ := tint.ReadTintEntry(file)

// Access colors
for i, color := range entry.Colors {
    fmt.Printf("Color %d: %s (%s)\n", i, color.String(), color.Hex())
}

// Export as CSS
css := entry.ToCSS("my-tint-name")
fmt.Println(css)
```

## Project Structure

```
evrFileTools/
├── cmd/
│   ├── evrtools/            # Package extraction/building CLI
│   ├── showtints/           # Tint display and CSS export CLI
│   └── texconv/             # DDS ↔ PNG texture converter
│       ├── main.go          # CLI and DDS format handling
│       ├── encoder.go       # BC compression (libsquish via CGo)
│       ├── squish_wrapper.cpp  # C++ wrapper for libsquish
│       └── squish_wrapper.h    # C header for CGo
├── pkg/
│   ├── archive/             # ZSTD archive format
│   │   ├── header.go        # Archive header (24 bytes)
│   │   ├── reader.go        # Streaming decompression
│   │   └── writer.go        # Streaming compression
│   ├── manifest/            # EVR manifest/package handling
│   │   ├── manifest.go      # Manifest types and binary encoding
│   │   ├── package.go       # Multi-part package extraction
│   │   ├── builder.go       # Package building from files
│   │   └── scanner.go       # Input directory scanning
│   ├── texture/             # Texture metadata and conversion
│   │   └── texture.go       # DDS header generation, BC conversion
│   ├── tint/                # Tint color processing
│   │   └── tint.go          # Tint parsing and CSS export
│   ├── audio/               # Audio reference structures
│   │   └── audio.go         # Audio reference parsing
│   └── asset/               # Asset reference structures
│       └── asset.go         # Asset reference parsing
├── docs/
│   ├── ASSET_FORMATS.md     # Complete format specifications
│   ├── TEXTURE_FORMAT_VERIFIED.md  # Texture format analysis
│   └── LEVEL_FORMAT_INVESTIGATION.md  # Level/scene format research
├── Makefile
└── go.mod
```

## Development

```bash
# Build
make build

# Run tests
make test

# Run benchmarks
make bench

# Format and lint
make check
```

## Documentation

- **[ASSET_FORMATS.md](docs/ASSET_FORMATS.md)** - Complete binary format specifications for all asset types
  - DDS Textures
  - Raw BC Textures  
  - Texture Metadata
  - Audio References
  - Asset References
  - Tints
  - Packages and Manifests
- **[TEXTURE_FORMAT_VERIFIED.md](docs/TEXTURE_FORMAT_VERIFIED.md)** - Detailed texture format analysis and verification
- **[LEVEL_FORMAT_INVESTIGATION.md](docs/LEVEL_FORMAT_INVESTIGATION.md)** - Level/scene format research (preliminary)

## Performance

The library uses several optimizations:

- **Direct binary encoding** instead of reflection-based `binary.Read/Write`
- **Pre-allocated buffers** for zero-allocation encoding paths
- **ZSTD context reuse** for ~4x faster decompression with zero allocations
- **Frame index maps** for O(1) file lookups during extraction
- **Directory caching** to minimize syscalls

Run benchmarks to see current performance:

```bash
go test -bench=. -benchmem ./pkg/...
```

## License

MIT License - see LICENSE file
