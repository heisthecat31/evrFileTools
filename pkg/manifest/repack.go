package manifest

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"github.com/DataDog/zstd"
)

// Pools to eliminate GC overhead
var (
	readPool         = sync.Pool{New: func() interface{} { return make([]byte, 0, 1024*1024) }}
	decompPool       = sync.Pool{New: func() interface{} { return make([]byte, 0, 4*1024*1024) }}
	compPool         = sync.Pool{New: func() interface{} { return make([]byte, 0, 1024*1024) }}
	constructionPool = sync.Pool{New: func() interface{} { return bytes.NewBuffer(make([]byte, 0, 4*1024*1024)) }}
)

type frameResult struct {
	index            int
	data             []byte
	err              error
	decompressedSize uint32
	isModified       bool
	shouldSkip       bool
	rawReadBuf       []byte
	decompBuf        []byte
}

type fcWrapper struct {
	index int
	fc    FrameContent
}

type packageWriter struct {
	fileHandle    *os.File
	pkgIndex      uint32
	outputDir     string
	pkgName       string
	created       map[uint32]bool
	currentOffset int64
	minPkgIndex   uint32
}

func (pw *packageWriter) write(manifest *Manifest, data []byte, decompressedSize uint32) error {
	os.MkdirAll(fmt.Sprintf("%s/packages", pw.outputDir), 0777)

	cEntry := Frame{}
	if len(manifest.Frames) > 0 {
		cEntry = manifest.Frames[len(manifest.Frames)-1]
	}
	activePackageNum := cEntry.PackageIndex

	// Ensure we don't write to protected original packages
	if activePackageNum < pw.minPkgIndex {
		activePackageNum = pw.minPkgIndex
	}

	// Ensure manifest knows about this package
	if manifest.Header.PackageCount <= activePackageNum {
		manifest.Header.PackageCount = activePackageNum + 1
	}

	// Check if the current frame forces a rotation, BUT only if we are still in the same package.
	// If we moved to a new package (activePackageNum > cEntry.PackageIndex), the offset of cEntry is irrelevant.
	if activePackageNum == cEntry.PackageIndex {
		if int64(cEntry.Offset)+int64(cEntry.CompressedSize)+int64(len(data)) > math.MaxInt32 {
			activePackageNum++
			manifest.Header.PackageCount = activePackageNum + 1
		}
	}

	// Open file and verify size constraints (handling existing files or rotation)
	for {
		if pw.fileHandle == nil || pw.pkgIndex != activePackageNum {
			if pw.fileHandle != nil {
				pw.fileHandle.Close()
			}

			currentPackagePath := fmt.Sprintf("%s/packages/%s_%d", pw.outputDir, pw.pkgName, activePackageNum)
			flags := os.O_RDWR | os.O_CREATE | os.O_APPEND

			if !pw.created[activePackageNum] {
				flags = os.O_RDWR | os.O_CREATE | os.O_TRUNC
				pw.created[activePackageNum] = true
			}

			f, err := os.OpenFile(currentPackagePath, flags, 0777)
			if err != nil {
				return err
			}
			pw.fileHandle = f
			pw.pkgIndex = activePackageNum

			stat, err := pw.fileHandle.Stat()
			if err != nil {
				return fmt.Errorf("stat package file: %w", err)
			}
			pw.currentOffset = stat.Size()
		}

		// Check if data fits in the current package
		if pw.currentOffset+int64(len(data)) > math.MaxInt32 {
			activePackageNum++
			manifest.Header.PackageCount = activePackageNum + 1
			continue // Retry with next package
		}
		break // Fits
	}

	if _, err := pw.fileHandle.Write(data); err != nil {
		return err
	}

	newEntry := Frame{
		PackageIndex:   activePackageNum,
		Offset:         uint32(pw.currentOffset),
		CompressedSize: uint32(len(data)),
		Length:         decompressedSize,
	}
	if int64(newEntry.Offset)+int64(newEntry.CompressedSize) > math.MaxInt32 {
		newEntry.Offset = 0
	}

	manifest.Frames = append(manifest.Frames, newEntry)
	incrementSection(&manifest.Header.Frames, 1)
	pw.currentOffset += int64(len(data))

	return nil
}

func (pw *packageWriter) close() {
	if pw.fileHandle != nil {
		pw.fileHandle.Close()
		pw.fileHandle = nil
	}
}

func incrementSection(s *Section, count int) {
	s.Count += uint64(count)
	s.ElementCount += uint64(count)
	s.Length += s.ElementSize * uint64(count)
}

func Repack(manifest *Manifest, fileMap [][]ScannedFile, outputDir, packageName, dataDir string) error {
	fmt.Println("Mapping modified files...")

	totalFiles := 0
	for _, chunk := range fileMap {
		totalFiles += len(chunk)
	}

	modifiedFilesLookupTable := make(map[[128]byte]ScannedFile, totalFiles)
	frameContentsLookupTable := make(map[[128]byte]FrameContent, manifest.Header.FrameContents.ElementCount)
	modifiedFrames := make(map[uint32]bool)

	for _, v := range manifest.FrameContents {
		buf := [128]byte{}
		binary.LittleEndian.PutUint64(buf[0:64], uint64(v.TypeSymbol))
		binary.LittleEndian.PutUint64(buf[64:128], uint64(v.FileSymbol))
		frameContentsLookupTable[buf] = v
	}

	for _, fileGroup := range fileMap {
		for _, v := range fileGroup {
			buf := [128]byte{}
			binary.LittleEndian.PutUint64(buf[0:64], uint64(v.TypeSymbol))
			binary.LittleEndian.PutUint64(buf[64:128], uint64(v.FileSymbol))

			if content, ok := frameContentsLookupTable[buf]; ok {
				modifiedFrames[content.FrameIndex] = true
				modifiedFilesLookupTable[buf] = v
			}
		}
	}
	fmt.Printf("Mapped %d files to modify.\n", len(modifiedFilesLookupTable))

	contentsByFrame := make(map[uint32][]fcWrapper)
	for k, v := range manifest.FrameContents {
		contentsByFrame[v.FrameIndex] = append(contentsByFrame[v.FrameIndex], fcWrapper{index: k, fc: v})
	}

	newManifest := *manifest
	newManifest.Frames = make([]Frame, 0)
	origFramesHeader := manifest.Header.Frames
	newManifest.Header.PackageCount = 1
	newManifest.Header.Frames = Section{
		Unk1:        origFramesHeader.Unk1,
		Unk2:        origFramesHeader.Unk2,
		ElementSize: 16,
	}

	packages := make(map[uint32]*os.File)
	for i := 0; i < int(manifest.Header.PackageCount); i++ {
		pFilePath := fmt.Sprintf("%s/packages/%s_%d", dataDir, packageName, i)
		f, err := os.Open(pFilePath)
		if err != nil {
			return fmt.Errorf("failed to open package %s: %v", pFilePath, err)
		}
		packages[uint32(i)] = f
		defer f.Close()
	}

	totalFrames := int(manifest.Header.Frames.ElementCount)
	lookaheadSize := runtime.NumCPU() * 16
	futureResults := make(chan chan frameResult, lookaheadSize)
	writer := &packageWriter{outputDir: outputDir, pkgName: packageName, created: make(map[uint32]bool)}
	defer writer.close()

	go func() {
		defer close(futureResults)
		for i := 0; i < totalFrames; i++ {
			resultChan := make(chan frameResult, 1)
			futureResults <- resultChan

			go func(idx int, ch chan frameResult) {
				v := manifest.Frames[idx]
				isMod := modifiedFrames[uint32(idx)]
				res := frameResult{index: idx, isModified: isMod, decompressedSize: v.Length}

				rawReadBuf := readPool.Get().([]byte)
				if cap(rawReadBuf) < int(v.CompressedSize) {
					rawReadBuf = make([]byte, int(v.CompressedSize))
				} else {
					rawReadBuf = rawReadBuf[:v.CompressedSize]
				}
				res.rawReadBuf = rawReadBuf

				activeFile := packages[v.PackageIndex]
				if v.CompressedSize > 0 {
					if _, err := activeFile.ReadAt(rawReadBuf, int64(v.Offset)); err != nil {
						if v.Length == 0 {
							res.shouldSkip = true
							ch <- res
							return
						}
						res.err = err
						ch <- res
						return
					}
				}

				if !isMod {
					res.data = rawReadBuf
					ch <- res
					return
				}

				decompBuf := decompPool.Get().([]byte)
				decompBytes, err := zstd.Decompress(decompBuf[:0], rawReadBuf)
				if err != nil {
					res.err = err
					ch <- res
					return
				}
				res.decompBuf = decompBytes

				bufObj := constructionPool.Get()
				constructionBuf := bufObj.(*bytes.Buffer)
				constructionBuf.Reset()
				defer constructionPool.Put(bufObj)

				sorted := make([]fcWrapper, 0)
				if contents, ok := contentsByFrame[uint32(idx)]; ok {
					sorted = append(sorted, contents...)
				}
				sort.Slice(sorted, func(a, b int) bool {
					return sorted[a].fc.DataOffset < sorted[b].fc.DataOffset
				})

				for j := 0; j < len(sorted); j++ {
					buf := [128]byte{}
					binary.LittleEndian.PutUint64(buf[0:64], uint64(sorted[j].fc.TypeSymbol))
					binary.LittleEndian.PutUint64(buf[64:128], uint64(sorted[j].fc.FileSymbol))

					if modFile, exists := modifiedFilesLookupTable[buf]; exists && modFile.FileSymbol != 0 {
						modData, err := os.ReadFile(modFile.Path)
						if err != nil {
							res.err = err
							ch <- res
							return
						}
						constructionBuf.Write(modData)
					} else {
						start := sorted[j].fc.DataOffset
						end := start + sorted[j].fc.Size
						constructionBuf.Write(decompBytes[start:end])
					}
				}

				compBuf := compPool.Get().([]byte)
				encodedData, err := zstd.CompressLevel(compBuf[:0], constructionBuf.Bytes(), zstd.BestSpeed)
				if err != nil {
					res.err = fmt.Errorf("compress frame: %w", err)
					ch <- res
					return
				}
				res.data = encodedData
				res.decompressedSize = uint32(constructionBuf.Len())

				ch <- res
			}(i, resultChan)
		}
	}()

	fmt.Println("Starting repack...")
	for resultCh := range futureResults {
		res := <-resultCh
		if res.err != nil {
			return res.err
		}

		if res.shouldSkip {
			if res.rawReadBuf != nil {
				readPool.Put(res.rawReadBuf)
			}
			if res.decompBuf != nil {
				decompPool.Put(res.decompBuf)
			}
			if res.isModified && res.data != nil {
				compPool.Put(res.data)
			}
			continue
		}

		if res.isModified {
			sorted := make([]fcWrapper, 0)
			if contents, ok := contentsByFrame[uint32(res.index)]; ok {
				sorted = append(sorted, contents...)
			}
			sort.Slice(sorted, func(a, b int) bool {
				return sorted[a].fc.DataOffset < sorted[b].fc.DataOffset
			})

			currentOffset := uint32(0)
			for j := 0; j < len(sorted); j++ {
				buf := [128]byte{}
				binary.LittleEndian.PutUint64(buf[0:64], uint64(sorted[j].fc.TypeSymbol))
				binary.LittleEndian.PutUint64(buf[64:128], uint64(sorted[j].fc.FileSymbol))

				size := sorted[j].fc.Size
				if modFile, exists := modifiedFilesLookupTable[buf]; exists && modFile.FileSymbol != 0 {
					size = modFile.Size
				}

				newManifest.FrameContents[sorted[j].index] = FrameContent{
					TypeSymbol: sorted[j].fc.TypeSymbol,
					FileSymbol: sorted[j].fc.FileSymbol,
					FrameIndex: sorted[j].fc.FrameIndex,
					DataOffset: currentOffset,
					Size:       size,
					Alignment:  sorted[j].fc.Alignment,
				}
				currentOffset += size
			}
		}

		if err := writer.write(&newManifest, res.data, res.decompressedSize); err != nil {
			return err
		}

		if res.isModified {
			if res.rawReadBuf != nil {
				readPool.Put(res.rawReadBuf)
			}
			if res.decompBuf != nil {
				decompPool.Put(res.decompBuf)
			}
			if res.data != nil {
				compPool.Put(res.data)
			}
		} else {
			if res.data != nil {
				readPool.Put(res.data)
			}
		}
	}

	writer.close()

	actualPkgCount := uint32(0)
	for {
		path := fmt.Sprintf("%s/packages/%s_%d", outputDir, packageName, actualPkgCount)
		if _, err := os.Stat(path); err != nil {
			break
		}
		actualPkgCount++
	}
	newManifest.Header.PackageCount = actualPkgCount

	for i := uint32(0); i < newManifest.Header.PackageCount; i++ {
		path := fmt.Sprintf("%s/packages/%s_%d", outputDir, packageName, i)
		stats, err := os.Stat(path)
		if err != nil {
			continue
		}
		newEntry := Frame{
			PackageIndex:   i,
			Offset:         uint32(stats.Size()),
			CompressedSize: 0, Length: 0,
		}
		newManifest.Frames = append(newManifest.Frames, newEntry)
		incrementSection(&newManifest.Header.Frames, 1)
	}

	newManifest.Frames = append(newManifest.Frames, Frame{})
	incrementSection(&newManifest.Header.Frames, 1)

	manifestDir := filepath.Join(outputDir, "manifests")
	if err := os.MkdirAll(manifestDir, 0755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}

	return WriteFile(filepath.Join(manifestDir, packageName), &newManifest)
}

// QuickRepack modifies the existing package files in-place by appending new frames
// and updating the manifest. This avoids rewriting the entire package set.
func QuickRepack(manifest *Manifest, fileMap [][]ScannedFile, dataDir, packageName string) error {
	manifestPath := filepath.Join(dataDir, "manifests", packageName)
	originalManifestPath := manifestPath + ".bak"

	// 1. Backup/Restore Logic: Ensure we have a clean original manifest
	// Check for legacy backup first
	if _, err := os.Stat(manifestPath + "_original"); err == nil {
		if _, err := os.Stat(originalManifestPath); os.IsNotExist(err) {
			os.Rename(manifestPath+"_original", originalManifestPath)
		}
	}

	if _, err := os.Stat(originalManifestPath); err == nil {
		// Backup exists, load it as the source of truth
		fmt.Println("Loading original manifest from backup...")
		origM, err := ReadFile(originalManifestPath)
		if err != nil {
			return fmt.Errorf("failed to read backup manifest: %w", err)
		}
		*manifest = *origM
	} else {
		// No backup, create one from current (assumed original)
		fmt.Println("Creating backup of original manifest...")
		input, err := os.ReadFile(manifestPath)
		if err == nil {
			os.WriteFile(originalManifestPath, input, 0644)
		}
	}

	minSafePackageIndex := manifest.Header.PackageCount

	// 2. Open Package
	pkgPath := filepath.Join(dataDir, "packages", packageName)
	srcPkg, err := OpenPackage(manifest, pkgPath)
	if err != nil {
		return fmt.Errorf("failed to open source package: %w", err)
	}
	defer srcPkg.Close()

	fmt.Println("Starting Quick Swap (In-Place Modification)...")

	totalFiles := 0
	for _, chunk := range fileMap {
		totalFiles += len(chunk)
	}

	modifiedFilesLookupTable := make(map[[128]byte]ScannedFile, totalFiles)
	frameContentsLookupTable := make(map[[128]byte]FrameContent, manifest.Header.FrameContents.ElementCount)

	for _, v := range manifest.FrameContents {
		buf := [128]byte{}
		binary.LittleEndian.PutUint64(buf[0:64], uint64(v.TypeSymbol))
		binary.LittleEndian.PutUint64(buf[64:128], uint64(v.FileSymbol))
		frameContentsLookupTable[buf] = v
	}

	for _, fileGroup := range fileMap {
		for _, v := range fileGroup {
			buf := [128]byte{}
			binary.LittleEndian.PutUint64(buf[0:64], uint64(v.TypeSymbol))
			binary.LittleEndian.PutUint64(buf[64:128], uint64(v.FileSymbol))

			if _, ok := frameContentsLookupTable[buf]; ok {
				modifiedFilesLookupTable[buf] = v
			}
		}
	}

	fmt.Println("Checking for identical files...")
	type checkItem struct {
		key [128]byte
		fc  FrameContent
		mod ScannedFile
	}
	var checks []checkItem
	for key, modFile := range modifiedFilesLookupTable {
		if fc, ok := frameContentsLookupTable[key]; ok {
			checks = append(checks, checkItem{key, fc, modFile})
		}
	}

	sort.Slice(checks, func(i, j int) bool {
		if checks[i].fc.FrameIndex != checks[j].fc.FrameIndex {
			return checks[i].fc.FrameIndex < checks[j].fc.FrameIndex
		}
		return checks[i].fc.DataOffset < checks[j].fc.DataOffset
	})

	skippedCount := 0
	for _, item := range checks {
		newData, err := os.ReadFile(item.mod.Path)
		if err != nil {
			return fmt.Errorf("read input %s: %w", item.mod.Path, err)
		}

		if uint32(len(newData)) == item.fc.Size {
			oldData, err := srcPkg.ReadContent(&item.fc)
			if err == nil && bytes.Equal(newData, oldData) {
				delete(modifiedFilesLookupTable, item.key)
				skippedCount++
			}
		}
	}

	if skippedCount > 0 {
		fmt.Printf("Skipped %d identical files.\n", skippedCount)
	}

	if len(modifiedFilesLookupTable) == 0 {
		fmt.Println("No files changed. Nothing to repack.")
		return nil
	}

	affectedFrames := make(map[uint32]bool)
	for key := range modifiedFilesLookupTable {
		if fc, ok := frameContentsLookupTable[key]; ok {
			affectedFrames[fc.FrameIndex] = true
		}
	}
	fmt.Printf("Mapped %d files to modify across %d frames.\n", len(modifiedFilesLookupTable), len(affectedFrames))

	contentsByFrame := make(map[uint32][]fcWrapper)
	for k, v := range manifest.FrameContents {
		if affectedFrames[v.FrameIndex] {
			contentsByFrame[v.FrameIndex] = append(contentsByFrame[v.FrameIndex], fcWrapper{index: k, fc: v})
		}
	}

	createdMap := make(map[uint32]bool)
	for i := uint32(0); i < manifest.Header.PackageCount; i++ {
		createdMap[i] = true
	}

	writer := &packageWriter{
		outputDir:   dataDir,
		pkgName:     packageName,
		created:     createdMap,
		minPkgIndex: minSafePackageIndex,
	}
	defer writer.close()

	var framesToProcess []int
	for idx := range affectedFrames {
		framesToProcess = append(framesToProcess, int(idx))
	}
	sort.Ints(framesToProcess)

	lookaheadSize := runtime.NumCPU() * 4
	futureResults := make(chan chan frameResult, lookaheadSize)

	go func() {
		defer close(futureResults)
		for _, idx := range framesToProcess {
			resultChan := make(chan frameResult, 1)
			futureResults <- resultChan

			go func(idx int, ch chan frameResult) {
				v := manifest.Frames[idx]
				res := frameResult{index: idx, isModified: true, decompressedSize: v.Length}

				rawReadBuf := readPool.Get().([]byte)
				if cap(rawReadBuf) < int(v.CompressedSize) {
					rawReadBuf = make([]byte, int(v.CompressedSize))
				} else {
					rawReadBuf = rawReadBuf[:v.CompressedSize]
				}
				res.rawReadBuf = rawReadBuf

				if int(v.PackageIndex) >= len(srcPkg.files) {
					res.err = fmt.Errorf("invalid package index %d", v.PackageIndex)
					ch <- res
					return
				}
				activeFile := srcPkg.files[v.PackageIndex]

				if v.CompressedSize > 0 {
					if _, err := activeFile.ReadAt(rawReadBuf, int64(v.Offset)); err != nil {
						res.err = err
						ch <- res
						return
					}
				}

				decompBuf := decompPool.Get().([]byte)
				decompBytes, err := zstd.Decompress(decompBuf[:0], rawReadBuf)
				if err != nil {
					res.err = err
					ch <- res
					return
				}
				res.decompBuf = decompBytes

				bufObj := constructionPool.Get()
				constructionBuf := bufObj.(*bytes.Buffer)
				constructionBuf.Reset()
				defer constructionPool.Put(bufObj)

				sorted := make([]fcWrapper, 0)
				if contents, ok := contentsByFrame[uint32(idx)]; ok {
					sorted = append(sorted, contents...)
				}
				sort.Slice(sorted, func(a, b int) bool {
					return sorted[a].fc.DataOffset < sorted[b].fc.DataOffset
				})

				for j := 0; j < len(sorted); j++ {
					buf := [128]byte{}
					binary.LittleEndian.PutUint64(buf[0:64], uint64(sorted[j].fc.TypeSymbol))
					binary.LittleEndian.PutUint64(buf[64:128], uint64(sorted[j].fc.FileSymbol))

					if modFile, exists := modifiedFilesLookupTable[buf]; exists && modFile.FileSymbol != 0 {
						modData, err := os.ReadFile(modFile.Path)
						if err != nil {
							res.err = err
							ch <- res
							return
						}
						constructionBuf.Write(modData)
					} else {
						start := sorted[j].fc.DataOffset
						end := start + sorted[j].fc.Size
						if end > uint32(len(decompBytes)) {
							res.err = fmt.Errorf("frame content out of bounds")
							ch <- res
							return
						}
						constructionBuf.Write(decompBytes[start:end])
					}
				}

				compBuf := compPool.Get().([]byte)
				encodedData, _ := zstd.CompressLevel(compBuf[:0], constructionBuf.Bytes(), zstd.BestSpeed)
				res.data = encodedData
				res.decompressedSize = uint32(constructionBuf.Len())

				ch <- res
			}(idx, resultChan)
		}
	}()

	fmt.Println("Writing modified frames...")
	for resultCh := range futureResults {
		res := <-resultCh
		if res.err != nil {
			return res.err
		}

		newFrameIndex := len(manifest.Frames)

		sorted := make([]fcWrapper, 0)
		if contents, ok := contentsByFrame[uint32(res.index)]; ok {
			sorted = append(sorted, contents...)
		}
		sort.Slice(sorted, func(a, b int) bool {
			return sorted[a].fc.DataOffset < sorted[b].fc.DataOffset
		})

		currentOffset := uint32(0)
		for j := 0; j < len(sorted); j++ {
			buf := [128]byte{}
			binary.LittleEndian.PutUint64(buf[0:64], uint64(sorted[j].fc.TypeSymbol))
			binary.LittleEndian.PutUint64(buf[64:128], uint64(sorted[j].fc.FileSymbol))

			size := sorted[j].fc.Size
			if modFile, exists := modifiedFilesLookupTable[buf]; exists && modFile.FileSymbol != 0 {
				size = modFile.Size
			}

			manifest.FrameContents[sorted[j].index] = FrameContent{
				TypeSymbol: sorted[j].fc.TypeSymbol,
				FileSymbol: sorted[j].fc.FileSymbol,
				FrameIndex: uint32(newFrameIndex),
				DataOffset: currentOffset,
				Size:       size,
				Alignment:  sorted[j].fc.Alignment,
			}
			currentOffset += size
		}

		if err := writer.write(manifest, res.data, res.decompressedSize); err != nil {
			return err
		}

		if res.rawReadBuf != nil {
			readPool.Put(res.rawReadBuf)
		}
		if res.decompBuf != nil {
			decompPool.Put(res.decompBuf)
		}
		if res.data != nil {
			compPool.Put(res.data)
		}
	}

	writer.close()

	fmt.Printf("Updating manifest: %s\n", manifestPath)
	return WriteFile(manifestPath, manifest)
}
