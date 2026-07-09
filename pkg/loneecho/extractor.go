package loneecho

import (
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// Extract processes a Lone Echo _data/win7 directory and copies/decompresses files to outputDir
func Extract(win7Dir string, outputDir string) error {
	fmt.Printf("Scanning Lone Echo directory: %s\n", win7Dir)

	var dll *syscall.LazyDLL
	var oodleDecompress *syscall.LazyProc
	hasOodle := false

	dllPaths := []string{
		"oo2core_8_win64.dll",
		"oodle_11_win64.dll",
		`J:\Lone Echo v3.17.4 -ARMGDDN\oo2core_8_win64.dll`,
		filepath.Join(win7Dir, "oodle_11_win64.dll"),
		filepath.Join(filepath.Dir(win7Dir), "oodle_11_win64.dll"),
		`J:\Lone Echo v3.17.4 -ARMGDDN\Lone Echo\bin\win7\oodle_11_win64.dll`,
	}

	for _, p := range dllPaths {
		if _, err := os.Stat(p); err == nil {
			dll = syscall.NewLazyDLL(p)
			if err := dll.Load(); err == nil {
				oodleDecompress = dll.NewProc("OodleLZ_Decompress")
				if oodleDecompress.Find() == nil {
					hasOodle = true
					fmt.Printf("Loaded Oodle decompressor from %s\n", p)

					// If this is an older Oodle (like oodle_11), it requires initialization
					if strings.Contains(p, "oodle_11") {
						getVTableClib := dll.NewProc("OodleMalloc_GetVTable_Clib")
						installVTable := dll.NewProc("OodleMalloc_InstallVTable")
						initDefault := dll.NewProc("Oodle_Init_Default")

						if getVTableClib.Find() == nil && installVTable.Find() == nil && initDefault.Find() == nil {
							vtable, _, _ := getVTableClib.Call()
							if vtable != 0 {
								installVTable.Call(vtable)
								initDefault.Call()
								fmt.Println("Successfully initialized Oodle 11 memory allocators")
							}
						}
					}
					break
				}
			}
		}
	}

	if !hasOodle {
		fmt.Println("WARNING: Oodle DLL not found! Files will be copied in their compressed state.")
	}

	count := 0
	err := filepath.Walk(win7Dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(win7Dir, path)
		if err != nil {
			return err
		}

		parts := strings.Split(filepath.ToSlash(relPath), "/")
		if len(parts) < 2 {
			return nil
		}

		// Strip 'primary' or 'GPU' prefix
		outRelPath := strings.Join(parts[1:], "/")
		outPath := filepath.Join(outputDir, outRelPath)

		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}

		if err := processFile(path, outPath, hasOodle, oodleDecompress); err != nil {
			fmt.Printf("Error processing %s: %v\n", relPath, err)
			return nil // continue
		}

		count++
		if count%1000 == 0 {
			fmt.Printf("Extracted %d files...\n", count)
		}

		return nil
	})

	if err != nil {
		return err
	}

	fmt.Printf("Extraction complete. %d files processed.\n", count)
	return nil
}

func processFile(src, dst string, hasOodle bool, oodleDecompress *syscall.LazyProc) error {
	data, err := ioutil.ReadFile(src)
	if err != nil {
		return err
	}

	// Check for COMPRESS header
	if len(data) >= 32 && string(data[:8]) == "COMPRESS" {
		if hasOodle {
			decompSize := binary.LittleEndian.Uint64(data[8:16])
			compSize := binary.LittleEndian.Uint64(data[16:24])

			payloadSize := uint64(len(data)) - 24
			if compSize > 0 && compSize < payloadSize {
				// compSize is the actual compressed payload size (some files have trailing data)
				payloadSize = compSize
			}

			// Only decompress if the payload is actually smaller than the uncompressed size
			// and starts with known Lone Echo Oodle magic bytes (0xB7, 0x8C, 0xCC, 0x00)
			isCompressed := payloadSize < decompSize && len(data) > 24 && (data[24] == 0xB7 || data[24] == 0x8C || data[24] == 0xCC || data[24] == 0x00)

			if isCompressed {
				outBuf := make([]byte, decompSize)

				if data[24] == 0x00 {
					// Chunked decompression for Lone Echo 1
					offset := uint64(24)
					outOffset := uint64(0)
					success := true

					for offset < uint64(len(data)) && outOffset < decompSize {
						if offset+16 > uint64(len(data)) {
							success = false
							break
						}
						
						_ = binary.LittleEndian.Uint64(data[offset : offset+8]) // decompOffset
						chunkCompSize := binary.LittleEndian.Uint64(data[offset+8 : offset+16])
						offset += 16

						if offset+chunkCompSize > uint64(len(data)) {
							success = false
							break
						}

						chunkDecompSize := uint64(0x40000) // 262144
						if outOffset+chunkDecompSize > decompSize {
							chunkDecompSize = decompSize - outOffset
						}

						ret, _, _ := oodleDecompress.Call(
							uintptr(unsafe.Pointer(&data[offset])),
							uintptr(chunkCompSize),
							uintptr(unsafe.Pointer(&outBuf[outOffset])),
							uintptr(chunkDecompSize),
							1, 0, 0, 0, 0, 0, 0, 0, 0, 3,
						)

						if ret != uintptr(chunkDecompSize) {
							fmt.Printf("Oodle chunk decomp failed for %s (Expected %d, got %d)\n", src, chunkDecompSize, ret)
							success = false
							break
						}

						outOffset += chunkDecompSize
						offset += chunkCompSize
					}

					if success && outOffset == decompSize {
						return ioutil.WriteFile(dst, outBuf, 0644)
					}
				} else {
					// Standard single-block decompression
					ret, _, _ := oodleDecompress.Call(
						uintptr(unsafe.Pointer(&data[24])),
						uintptr(payloadSize),
						uintptr(unsafe.Pointer(&outBuf[0])),
						uintptr(decompSize),
						1, 0, 0, 0, 0, 0, 0, 0, 0, 3,
					)

					if ret == uintptr(decompSize) {
						return ioutil.WriteFile(dst, outBuf, 0644)
					}
					fmt.Printf("Oodle decomp failed for %s (ret %d)\n", src, ret)
				}

				runtime.KeepAlive(data)
				runtime.KeepAlive(outBuf)
			}

			// If it's uncompressed or decompression failed, just extract the raw bytes directly
			if uint64(len(data)-24) >= decompSize {
				return ioutil.WriteFile(dst, data[24:24+decompSize], 0644)
			}

			// Fallback: copy everything if we can't even extract raw bytes cleanly
			return ioutil.WriteFile(dst, data, 0644)
		}
	}

	// Fallback to plain copy
	return ioutil.WriteFile(dst, data, 0644)
}
