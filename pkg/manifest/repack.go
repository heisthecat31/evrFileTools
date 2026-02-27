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
	fileHandle *os.File
	pkgIndex   uint32
	outputDir  string
	pkgName    string
	created    map[uint32]bool
}

func (pw *packageWriter) write(manifest *Manifest, data []byte, decompressedSize uint32) error {
	os.MkdirAll(fmt.Sprintf("%s/packages", pw.outputDir), 0777)

	cEntry := Frame{}
	activePackageNum := uint32(0)
	if len(manifest.Frames) > 0 {
		cEntry = manifest.Frames[len(manifest.Frames)-1]
		activePackageNum = cEntry.PackageIndex
	}

	if int64(cEntry.Offset)+int64(cEntry.CompressedSize)+int64(len(data)) > math.MaxInt32 {
		activePackageNum++
		manifest.Header.PackageCount = activePackageNum + 1
	}

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
	}

	if _, err := pw.fileHandle.Write(data); err != nil {
		return err
	}

	newEntry := Frame{
		PackageIndex:   activePackageNum,
		Offset:         cEntry.Offset + cEntry.CompressedSize,
		CompressedSize: uint32(len(data)),
		Length:         decompressedSize,
	}
	if int64(newEntry.Offset)+int64(newEntry.CompressedSize) > math.MaxInt32 {
		newEntry.Offset = 0
	}

	manifest.Frames = append(manifest.Frames, newEntry)
	incrementSection(&manifest.Header.Frames, 1)

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
				encodedData, _ := zstd.CompressLevel(compBuf[:0], constructionBuf.Bytes(), zstd.BestSpeed)
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
