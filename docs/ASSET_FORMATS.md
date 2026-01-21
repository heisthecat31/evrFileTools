# Echo VR Asset Format Specifications

Complete binary format specifications for all Echo VR asset types extracted from game packages.

## Table of Contents

1. [DDS Textures](#1-dds-textures)
2. [Raw BC Textures](#2-raw-bc-textures)
3. [Texture Metadata](#3-texture-metadata)
4. [Audio References](#4-audio-references)
5. [Asset References](#5-asset-references)
6. [Tints](#6-tints)
7. [Packages and Manifests](#7-packages-and-manifests)

---

## 1. DDS Textures

**Type Symbol**: `0xbeac1969cb7b8861`  
**Asset Type ID**: `0x11b1393ff`  
**File Count**: 12,275 files  
**Total Size**: ~2.8 GB  
**Format**: Standard Microsoft DirectDraw Surface (DDS) with DX10 extension

### Description

Standard DDS texture files with complete headers. These can be opened with any DDS-compatible tool.

### Magic Number

`0x20534444` ("DDS " in ASCII)

### Header Structure

| Offset | Size | Type   | Field            | Description                  |
|--------|------|--------|------------------|------------------------------|
| 0x00   | 4    | uint32 | magic            | "DDS " (0x20534444)          |
| 0x04   | 4    | uint32 | size             | Header size (124)            |
| 0x08   | 4    | uint32 | flags            | Surface description flags    |
| 0x0C   | 4    | uint32 | height           | Texture height in pixels     |
| 0x10   | 4    | uint32 | width            | Texture width in pixels      |
| 0x14   | 4    | uint32 | pitchOrLinearSize | Pitch or linear size        |
| 0x18   | 4    | uint32 | depth            | Texture depth                |
| 0x1C   | 4    | uint32 | mipMapCount      | Number of mipmap levels      |
| ...    | ...  | ...    | ...              | Additional DDS header fields |

### DX10 Extension

Echo VR DDS files use the DX10 extension header (20 bytes after main header):

| Offset | Size | Type   | Field              | Description                |
|--------|------|--------|--------------------|----------------------------|
| 0x80   | 4    | uint32 | dxgiFormat         | DXGI_FORMAT enum value     |
| 0x84   | 4    | uint32 | resourceDimension  | 3 = TEXTURE2D              |
| 0x88   | 4    | uint32 | miscFlag           | Additional flags           |
| 0x8C   | 4    | uint32 | arraySize          | Array size (1 for normal)  |
| 0x90   | 4    | uint32 | miscFlags2         | Additional flags           |

### Common DXGI Formats

- `71` - BC1_UNORM (DXT1)
- `77` - BC3_UNORM (DXT5)
- `98` - BC7_UNORM
- `99` - BC7_UNORM_SRGB

### Parsing

Use standard DDS libraries:

**Go:**
```go
import "github.com/ftrvxmtrx/dds"

img, err := dds.Decode(file)
```

**Python:**
```python
from PIL import Image
from PIL.ImageFile import ImageFile

img = Image.open("texture.dds")
```

**C++:**
```cpp
#include <DirectXTex.h>

DirectX::ScratchImage image;
DirectX::LoadFromDDSFile(L"texture.dds", DirectX::DDS_FLAGS_NONE, nullptr, image);
```

---

## 2. Raw BC Textures

**Type Symbol**: `0x7f5bc1cf8ce51ffd`  
**Asset Type ID**: `0x11b1393fe`  
**File Count**: 17,226 files  
**Total Size**: ~3.2 GB  
**Format**: Headerless BC-compressed texture data

### Description

BC (Block Compressed) texture data without DDS headers. These files contain only the compressed pixel data and require a matching metadata file to reconstruct a valid DDS file.

### Structure

```
[Raw BC compressed data, no header]
```

- No magic number
- No metadata in file
- Size varies by texture dimensions and format
- Requires companion metadata file for interpretation

### Converting to DDS

To convert raw BC data to viewable DDS:

1. Locate matching metadata file (same asset symbol, type `0x2f6e61706a2c8f35`)
2. Parse metadata to get dimensions, format, mip levels
3. Generate DDS header with DX10 extension
4. Prepend header to raw data

**Example (using evrFileTools):**

```go
import (
    "github.com/EchoTools/evrFileTools/pkg/texture"
    "io/ioutil"
)

// Read metadata
metaData, _ := ioutil.ReadFile("texture.meta")
meta, _ := texture.ParseMetadata(bytes.NewReader(metaData))

// Read raw BC data
rawData, _ := ioutil.ReadFile("texture.raw")

// Convert to DDS
ddsData, _ := texture.ConvertRawBCToDDS(rawData, meta)

// Write DDS file
ioutil.WriteFile("texture.dds", ddsData, 0644)
```

### Block Compression Formats

| Format | Block Size | Bytes per Block | Description              |
|--------|------------|-----------------|--------------------------|
| BC1    | 4x4        | 8               | RGB (DXT1)               |
| BC2    | 4x4        | 16              | RGBA (DXT3)              |
| BC3    | 4x4        | 16              | RGBA (DXT5)              |
| BC4    | 4x4        | 8               | Single channel           |
| BC5    | 4x4        | 16              | Two channels (normals)   |
| BC6H   | 4x4        | 16              | HDR                      |
| BC7    | 4x4        | 16              | High quality RGBA        |

---

## 3. Texture Metadata

**Type Symbol**: `0x2f6e61706a2c8f35`  
**File Count**: 12,275 files  
**File Size**: 256 bytes (fixed)  
**Format**: Binary structure

### Description

Texture metadata files describe the properties of texture assets. Each metadata file corresponds to either a DDS texture or raw BC texture.

### Binary Structure

| Offset | Size | Type     | Field       | Description                    |
|--------|------|----------|-------------|--------------------------------|
| 0x00   | 4    | uint32   | width       | Texture width in pixels        |
| 0x04   | 4    | uint32   | height      | Texture height in pixels       |
| 0x08   | 4    | uint32   | mipLevels   | Number of mipmap levels        |
| 0x0C   | 4    | uint32   | dxgiFormat  | DXGI_FORMAT enum value         |
| 0x10   | 4    | uint32   | ddsFileSize | Expected size with DDS header  |
| 0x14   | 4    | uint32   | rawFileSize | Size of raw BC data            |
| 0x18   | 4    | uint32   | flags       | Texture flags                  |
| 0x1C   | 4    | uint32   | arraySize   | Array size (1 for normal)      |
| 0x20   | 224  | byte[224]| reserved    | Reserved/padding               |

**Total**: 256 bytes

### Parsing Example

```go
import (
    "github.com/EchoTools/evrFileTools/pkg/texture"
    "os"
)

file, _ := os.Open("texture.meta")
defer file.Close()

meta, _ := texture.ParseMetadata(file)

fmt.Printf("Texture: %dx%d, %d mips, format=%s\n",
    meta.Width, meta.Height, meta.MipLevels,
    texture.FormatName(meta.DXGIFormat))
```

### Field Details

**dxgiFormat**: DXGI_FORMAT enum values (see DDS Textures section)

**flags**: Bit flags indicating texture properties
- Bit 0: sRGB color space
- Bit 1: Normal map
- Bit 2-31: Reserved

**arraySize**: Typically 1 for standard textures, >1 for texture arrays

---

## 4. Audio References

**Type Symbol**: `0x38ee951a26fb816a`  
**File Count**: 119 files  
**File Sizes**: Variable  
**Format**: Binary reference structure

### Description

Audio reference files contain indices or pointers to audio assets within the game's audio system. These link audio events to actual audio data.

### Binary Structure

| Offset | Size | Type   | Field          | Description                      |
|--------|------|--------|----------------|----------------------------------|
| 0x00   | 8    | uint64 | guidType       | Type GUID (0x38ee951a26fb816a)   |
| 0x08   | 8    | uint64 | assetReference | Reference to audio asset         |
| 0x10   | 4    | uint32 | count          | Number of entries or flags       |
| 0x14   | 4    | uint32 | flags          | Additional flags                 |
| 0x18   | ...  | byte[] | reserved       | Additional data (variable size)  |

### Parsing Example

```go
import (
    "github.com/EchoTools/evrFileTools/pkg/audio"
    "os"
)

file, _ := os.Open("audio_ref")
defer file.Close()

ref, _ := audio.ParseAudioReference(file)

fmt.Printf("Audio: guid=%016x, asset=%016x, count=%d\n",
    ref.GUIDType, ref.AssetReference, ref.Count)
```

---

## 5. Asset References

**Type Symbol**: `0xca6cd085401cbc87`  
**File Count**: 2,109 files  
**File Sizes**: 88, 96, 120, 136, 200, 296 bytes  
**Format**: Binary reference structure

### Description

Asset reference files link assets together, forming the game's asset dependency graph. Different sizes indicate different reference types.

### Reference Types by Size

| Size | Type     | Count | Description                        |
|------|----------|-------|------------------------------------|
| 88   | Material | ~500  | Material to texture/tint links     |
| 96   | Tint     | ~400  | Tint reference (not tint data)     |
| 120  | Texture  | ~600  | Texture to metadata links          |
| 136  | Dual     | ~400  | Dual asset reference               |
| 200+ | Complex  | ~209  | Complex multi-asset references     |

### Common Structure

| Offset | Size | Type   | Field         | Description                    |
|--------|------|--------|---------------|--------------------------------|
| 0x00   | 8    | uint64 | referenceGUID | Asset being referenced         |
| 0x08   | 8    | uint64 | targetGUID    | Target asset                   |
| 0x10   | 4    | uint32 | flags         | Reference flags                |
| 0x14   | ...  | byte[] | data          | Type-specific data             |

### Parsing Example

```go
import (
    "github.com/EchoTools/evrFileTools/pkg/asset"
    "os"
)

file, _ := os.Open("asset_ref")
defer file.Close()

ref, _ := asset.ParseReference(file)

fmt.Printf("Reference: type=%s, ref=%016x, target=%016x\n",
    ref.Type, ref.ReferenceGUID, ref.TargetGUID)
```

---

## 6. Tints

**File Size**: 96 bytes (fixed)  
**Type**: Stored within CR15NetRewardItemCS component data  
**Format**: Binary structure with RGBA float32 colors

### Description

Tints are cosmetic color schemes applied to player chassis. Each tint contains 6 RGBA color values representing main and accent colors for multiple material layers.

### Binary Structure

| Offset | Size | Type     | Field      | Description                      |
|--------|------|----------|------------|----------------------------------|
| 0x00   | 8    | uint64   | resourceID | Symbol64 hash identifying tint   |
| 0x08   | 16   | Color    | color0     | Main color 1 (RGBA float32)      |
| 0x18   | 16   | Color    | color1     | Accent color 1 (RGBA float32)    |
| 0x28   | 16   | Color    | color2     | Main color 2 (RGBA float32)      |
| 0x38   | 16   | Color    | color3     | Accent color 2 (RGBA float32)    |
| 0x48   | 16   | Color    | color4     | Body color (RGBA float32)        |
| 0x58   | 8    | byte[8]  | reserved   | Reserved/padding                 |

**Total**: 96 bytes

### Color Structure

Each color is 16 bytes (4 float32 values):

| Offset | Size | Type    | Field | Description          |
|--------|------|---------|-------|----------------------|
| +0     | 4    | float32 | r     | Red (0.0 - 1.0)      |
| +4     | 4    | float32 | g     | Green (0.0 - 1.0)    |
| +8     | 4    | float32 | b     | Blue (0.0 - 1.0)     |
| +12    | 4    | float32 | a     | Alpha (0.0 - 1.0)    |

### Known Tints

Some identified tint names (Symbol64 → name):

- `0x74d228d09dc5dc86` → "rwd_tint_0000"
- `0x74d228d09dc5dc87` → "rwd_tint_0001"
- `0x3e474b60a9416aca` → "rwd_tint_s1_a_default"
- `0x43ac219540f9df74` → "rwd_tint_s1_b_default"

(See `pkg/tint/tint.go` for complete list of 48 known tints)

### Parsing Example

```go
import (
    "github.com/EchoTools/evrFileTools/pkg/tint"
    "os"
)

file, _ := os.Open("tint_file")
defer file.Close()

entry, _ := tint.ReadTintEntry(file)

fmt.Printf("Tint: %016x\n", entry.ResourceID)
for i, color := range entry.Colors {
    fmt.Printf("  Color %d: %s (%s)\n", i, color.String(), color.Hex())
}
// Prints 5 colors (0-4)
```

### CSS Export

```bash
showtints --css extracted/ > tints.css
```

Output format:
```css
:root {
  --tint-rwd-tint-0000-main-1:         rgba(255, 128, 64, 1.000);
  --tint-rwd-tint-0000-accent-1:       rgba(200, 100, 50, 1.000);
  --tint-rwd-tint-0000-main-2:         rgba(255, 128, 64, 1.000);
  --tint-rwd-tint-0000-accent-2:       rgba(200, 100, 50, 1.000);
  --tint-rwd-tint-0000-body:           rgba(180, 180, 180, 1.000);
}
```

---

## 7. Packages and Manifests

Echo VR assets are stored in multi-part package files with manifests describing their contents.

### Package Structure

```
_data/5932408047/rad15/win10/
├── manifests/
│   └── <package_symbol>          # Manifest file
└── packages/
    ├── <package_symbol>.0        # Package part 0
    ├── <package_symbol>.1        # Package part 1
    └── ...                       # Additional parts
```

### Manifest Format

See `pkg/manifest/manifest.go` for detailed structures:

- **Header** (192 bytes): Package metadata and section descriptors
- **FrameContents**: File entries with location information
- **Metadata**: Asset type and symbol mapping
- **Frames**: Compressed frame descriptors

### Frame Compression

Package files are divided into frames, each independently compressed with ZSTD:

| Field           | Type   | Description                     |
|-----------------|--------|---------------------------------|
| PackageIndex    | uint32 | Which package file (0, 1, ...) |
| Offset          | uint32 | Byte offset in package file     |
| CompressedSize  | uint32 | Compressed frame size           |
| Length          | uint32 | Decompressed frame size         |

### Extraction

```bash
evrtools -mode extract \
    -data ./path/to/_data \
    -package <package_symbol> \
    -output ./extracted
```

Output structure:
```
extracted/
├── <type_symbol>/
│   ├── <file_symbol_1>
│   ├── <file_symbol_2>
│   └── ...
```

---

## Asset Type Summary

| Type Symbol           | Asset Type ID | Count  | Size        | Description          |
|-----------------------|---------------|--------|-------------|----------------------|
| 0xbeac1969cb7b8861    | 0x11b1393ff   | 12,275 | ~2.8 GB     | DDS Textures         |
| 0x7f5bc1cf8ce51ffd    | 0x11b1393fe   | 17,226 | ~3.2 GB     | Raw BC Textures      |
| 0x2f6e61706a2c8f35    | ?             | 12,275 | 3.14 MB     | Texture Metadata     |
| 0x38ee951a26fb816a    | ?             | 119    | Variable    | Audio References     |
| 0xca6cd085401cbc87    | ?             | 2,109  | Variable    | Asset References     |
| (Various)             | ?             | ?      | 96 bytes    | Tints                |

**Total Identified**: ~44,004 files (~6 GB)

---

## Tools

### evrFileTools

Go library and CLI for working with Echo VR packages:

```bash
# Install
go install github.com/EchoTools/evrFileTools/cmd/evrtools@latest

# Extract package
evrtools -mode extract -data ./_data -package <symbol> -output ./out

# Build package
evrtools -mode build -input ./files -output ./out -package mypackage
```

### showtints

Display and export tint color data:

```bash
# Show all tints
showtints ./extracted

# Export as CSS
showtints --css --known ./extracted > tints.css

# Summary only
showtints --summary ./extracted
```

---

## References

- Binary analysis performed using Ghidra on `echovr.exe`
- File format reverse engineering from extracted game packages
- Symbol64 hash to name mappings from nakama server data
- Microsoft DirectDraw Surface (DDS) specification
- DXGI_FORMAT enum from DirectX SDK

---

## Contributing

Found an error or have additional information? Contributions welcome!

Repository: [github.com/EchoTools/evrFileTools](https://github.com/EchoTools/evrFileTools)
