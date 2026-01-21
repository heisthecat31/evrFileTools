# EchoVR Level/Scene Format Investigation

**Status**: Preliminary investigation complete  
**Date**: 2026-01-20  
**Investigator**: AI Assistant  

## Summary

Level files in EchoVR (e.g., `social_2.0`) are stored as custom binary formats containing scene/mesh data. This is a complex custom format requiring extensive reverse engineering work.

## Findings

### File Characteristics

**Sample file**: `_extracted/3b5db8af43546d40/43e2da7914642604`
- **Size**: 105 KB (107,688 bytes)
- **Type**: Binary data (no magic signature detected)
- **Structure**: Contains IEEE 754 floats and structured binary data

### Binary Structure Observations

From hex analysis (`xxd` output):

```
Offset    Pattern                 Interpretation
--------  ----------------------  --------------
0x00      00 00 00 00 ...         Possible header/version
0x80-0xF0 0x3f800000 (repeated)   IEEE 754 float 1.0 (common in 3D math)
0x120+    0x00408043              Float 3.0 (coordinate?)
0x130+    0x3e                    Float 0.x (UV coordinate?)
```

**Float patterns suggest**:
- 3D coordinates (x, y, z)
- Normal vectors (normalized to 1.0)
- UV texture coordinates (0.0-1.0 range)
- Transformation matrices (identity = all 1.0)

### Ghidra Analysis

**Key Functions Found** (echovr.exe @ port 8193):

| Function | Address | Description |
|----------|---------|-------------|
| `LoadLevelSet` | `0x1402fe430` | Level set loading |
| `RenderLevelMeshes` | `0x140d46c20` | Level mesh rendering |
| `RenderLevelLights` | `0x1404b0800` | Level light rendering |
| `CGScene::ActivateGroup` | `0x1400d3b40` | Scene group activation |
| `CGScene::DeactivateGroup` | `0x1416762f0` | Scene group deactivation |
| `UpdateMeshRenderData` | `0x1401ebb30` | Mesh render data update |

**Key Strings Found**:
- `"OlPrEfIxsocial_2.0"` @ `0x1416d8d50` - Level identifier
- `"OlPrEfIxsocial_2.0_private"` @ `0x1416e303c` - Private lobby variant
- `"OlPrEfIxsocial_2.0_npe"` @ `0x1416e3068` - NPE (new player experience) variant

### LoadLevelSet Function Analysis

**Signature**:
```cpp
void __thiscall NRadEngine::CncaGame::LoadLevelSet(
    CncaGame *this,
    ELevelLoadChannel param_1,
    CLevelLoadSet *param_2
)
```

**Key structures**:
- `CLevelLoadSet` - Level set descriptor
- `ELevelLoadChannel` - Loading channel enum
- Offset `+0x150` - Level set count
- Offset `+0x120` - Level set array pointer

### Likely Format Structure

Based on float patterns and game engine conventions:

```
[Header]
- Magic/Version: 8-16 bytes
- Metadata: counts, offsets, sizes

[Vertex Data]
- Position: float3 (x, y, z)
- Normal: float3 (nx, ny, nz)
- UV: float2 (u, v)
- Additional attributes (color, tangent, etc.)

[Index Buffer]
- Triangle indices (uint16 or uint32)

[Material Data]
- Texture GUIDs
- Shader parameters
- Material properties

[Transform Data]
- Matrices (4x4 float)
- Position, rotation, scale

[Scene Graph]
- Object hierarchy
- Bounding volumes
- LOD levels
```

## Next Steps (Future Work)

### Priority 1: Binary Format Parsing
1. **Identify header structure**
   - Find magic number or version field
   - Parse counts (vertex count, triangle count, etc.)
   - Locate data offsets

2. **Vertex format analysis**
   - Determine vertex stride (bytes per vertex)
   - Identify attribute layout
   - Confirm float vs half-float usage

3. **Index buffer location**
   - Find index data start
   - Determine index size (16-bit vs 32-bit)
   - Verify triangle list vs strip

### Priority 2: Ghidra Deep Dive
1. **LoadLevelSet decomp analysis**
   - Trace calls to resource loaders
   - Find format version checks
   - Identify parser functions

2. **CGScene analysis**
   - Understand scene graph structure
   - Find mesh loading code path
   - Locate format readers

3. **Resource system**
   - `LoadResource` @ `0x1405bbae0`
   - `DestroyResource` @ `0x1404c85d0`
   - Track from file I/O to GPU

### Priority 3: Runtime Debugging
1. **Memory inspection**
   - Attach debugger to running EchoVR
   - Break on `LoadLevelSet`
   - Inspect buffer contents after load

2. **File system hooking**
   - Intercept level file reads
   - Log raw binary data
   - Compare multiple level files

3. **Mesh extraction**
   - Dump loaded meshes from GPU
   - Use tools like RenderDoc or PIX
   - Reverse map GPU data to file format

## Known Challenges

1. **Custom format** - Not a standard format (FBX, OBJ, GLTF, etc.)
2. **Binary complexity** - Multiple sections with internal pointers
3. **Versioning** - Format may differ by game version
4. **Compression** - Data may be compressed (ZSTD, LZ4, etc.)
5. **Endianness** - Likely little-endian (x86-64) but needs confirmation

## Estimated Effort

- **Basic parser**: 20-40 hours
- **Full format specification**: 60-100 hours
- **Mesh extraction tool**: 40-60 hours
- **Round-trip (edit & reimport)**: 80-120 hours

## Value vs. Cost

**Benefits**:
- Level editing capabilities
- Custom maps/arenas
- Asset research & documentation

**Costs**:
- High time investment
- Limited immediate use case
- Lower priority than texture system

**Recommendation**: Defer until texture modding workflow is validated with live game testing. Focus on higher-value deliverables first.

## References

- **File sample**: `_extracted/3b5db8af43546d40/43e2da7914642604`
- **Ghidra project**: `EchoVR_6323983201049540` (port 8193)
- **Key function**: `LoadLevelSet` @ `0x1402fe430`
- **Related docs**: `ASSET_TO_MEMORY_PIPELINE.md`, `ASSET_FORMATS.md`

## Related Asset Types

Level files likely reference:
1. **Textures** (DDS) - Already solved
2. **Materials** (asset references) - Already parsed
3. **Audio** (audio references) - Already parsed
4. **Animations** - Unknown format
5. **Physics meshes** - Unknown format
6. **Collision data** - Unknown format

---

**Status**: Investigation complete. Parser implementation deferred pending use case validation.
