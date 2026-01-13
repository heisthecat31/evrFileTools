package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	evrm "github.com/goopsie/evrFileTools/evrManifests"
)

// --- Types ---

type CompressedHeader struct {
	Magic            [4]byte
	HeaderSize       uint32
	UncompressedSize uint64
	CompressedSize   uint64
}

type newFile struct {
	TypeSymbol       int64
	FileSymbol       int64
	ModifiedFilePath string
	FileSize         uint32
}

type fcWrapper struct {
	index int
	fc    evrm.FrameContents
}

type frameResult struct {
	index            int
	data             []byte // Final compressed data to write
	err              error
	decompressedSize uint32
	isModified       bool
	shouldSkip       bool
	
	// References for recycling
	rawReadBuf       []byte
	decompBuf        []byte
}

// --- Globals & Flags ---

var (
	encoder *zstd.Encoder
	decoder *zstd.Decoder
	
	// Pools to eliminate GC overhead
	// 1. For reading compressed chunks from source packages
	readPool = sync.Pool{New: func() interface{} { return make([]byte, 0, 1024*1024) }} 
	// 2. For holding decompressed data (large)
	decompPool = sync.Pool{New: func() interface{} { return make([]byte, 0, 4*1024*1024) }} 
	// 3. For holding final re-compressed data
	compPool = sync.Pool{New: func() interface{} { return make([]byte, 0, 1024*1024) }}
	// 4. For constructing patched files (bytes.Buffer)
	constructionPool = sync.Pool{New: func() interface{} { return bytes.NewBuffer(make([]byte, 0, 4*1024*1024)) }}
)

const compressionLevel = zstd.SpeedFastest

var (
	mode                     string
	manifestType             string
	packageName              string
	dataDir                  string
	inputDir                 string
	outputDir                string
	outputPreserveGroups     bool
	texturesOnly             bool
	help                     bool
	ignoreOutputRestrictions bool
	
	createdPackages map[uint32]bool
)

func init() {
	flag.StringVar(&mode, "mode", "", "must be one of the following: 'extract', 'build', 'replace', 'jsonmanifest'")
	flag.StringVar(&manifestType, "manifestType", "5932408047-EVR", "See readme for updated list of manifest types.")
	flag.StringVar(&packageName, "packageName", "package", "File name of package, e.g. 48037dc70b0ecab2, 2b47aab238f60515, etc.")
	flag.StringVar(&dataDir, "dataDir", "", "Path of directory containing 'manifests' & 'packages' in ready-at-dawn-echo-arena/_data")
	flag.StringVar(&inputDir, "inputDir", "", "Path of directory containing modified files")
	flag.StringVar(&outputDir, "outputDir", "", "Path of directory to place modified package & manifest files")
	flag.BoolVar(&outputPreserveGroups, "outputPreserveGroups", false, "If true, preserve groups during '-mode extract'")
	flag.BoolVar(&texturesOnly, "texturesonly", false, "If true, only extracts specific texture folders")
	flag.BoolVar(&ignoreOutputRestrictions, "ignoreOutputRestrictions", false, "Allows non-empty outputDir to be used.")
	flag.BoolVar(&help, "help", false, "Print usage")
	flag.Parse()

	createdPackages = make(map[uint32]bool)

	if help {
		flag.Usage()
		os.Exit(0)
	}

	if mode == "" || outputDir == "" {
		flag.Usage()
		os.Exit(1)
	}
	
	if mode == "build" && inputDir == "" {
		fmt.Println("'-mode build' requires '-inputDir'")
		os.Exit(1)
	}

	os.MkdirAll(outputDir, 0777)

	isOutputDirEmpty := func() bool {
		entries, err := os.ReadDir(outputDir)
		if err != nil {
			return false
		}
		return len(entries) == 0
	}()

	if !isOutputDirEmpty && !ignoreOutputRestrictions {
		fmt.Println("Output directory is not empty. Use '-ignoreOutputRestrictions' to override this restriction.")
		os.Exit(1)
	}

	// ZSTD Init
	var err error
	decoder, err = zstd.NewReader(nil)
	if err != nil { panic(err) }
	encoder, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(compressionLevel))
	if err != nil { panic(err) }
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("CRITICAL ERROR:", r)
		}
	}()

	if mode == "build" {
		fmt.Println("Building list of files to package...")
		files, err := scanPackageFiles()
		if err != nil {
			fmt.Printf("failed to scan %s: %v\n", inputDir, err)
			return
		}
		if err := rebuildPackageManifestCombo(files); err != nil {
			fmt.Println(err)
		}
		return
	}

	b, err := os.ReadFile(dataDir + "/manifests/" + packageName)
	if err != nil {
		fmt.Println("Failed to open manifest file, check dataDir path")
		return
	}

	compHeader := CompressedHeader{}
	headerSize := binary.Size(compHeader)
	if len(b) < headerSize {
		fmt.Println("Manifest file too small")
		return
	}

	decompBytes, err := decompressZSTD(b[headerSize:])
	if err != nil {
		fmt.Println("Failed to decompress manifest:", err)
		return
	}

	buf := bytes.NewReader(b)
	if err := binary.Read(buf, binary.LittleEndian, &compHeader); err != nil {
		fmt.Println("Failed to read manifest header:", err)
		return
	}

	manifest, err := evrm.MarshalManifest(decompBytes, manifestType)
	if err != nil {
		fmt.Println("Error unmarshalling manifest:", err)
		return
	}

	if mode == "extract" {
		if err := extractFilesFromPackage(manifest); err != nil {
			fmt.Println("Error extracting files:", err)
		}
	} else if mode == "replace" {
		fmt.Println("Scanning input files...")
		files, err := scanPackageFiles()
		if err != nil {
			fmt.Printf("Failed to scan %s: %v\n", inputDir, err)
			return
		}
		if err := replaceFiles(files, manifest); err != nil {
			fmt.Println("Error in replaceFiles:", err)
		}
	} else if mode == "jsonmanifest" {
		jFile, _ := os.Create("manifestdebug.json")
		jBytes, _ := json.MarshalIndent(manifest, "", " ")
		jFile.Write(jBytes)
		jFile.Close()
	}
}

// --- Writer Helper ---
type packageWriter struct {
	fileHandle *os.File
	pkgIndex   uint32
}

func (pw *packageWriter) write(manifest *evrm.EvrManifest, data []byte, decompressedSize uint32) error {
	os.MkdirAll(fmt.Sprintf("%s/packages", outputDir), 0777)

	cEntry := evrm.Frame{}
	activePackageNum := uint32(0)
	if len(manifest.Frames) > 0 {
		cEntry = manifest.Frames[len(manifest.Frames)-1]
		activePackageNum = cEntry.CurrentPackageIndex
	}
	
	// Check rotation
	if int(cEntry.CurrentOffset+cEntry.CompressedSize)+len(data) > math.MaxInt32 {
		activePackageNum++
		manifest.Header.PackageCount = activePackageNum + 1
	}

	// File Handling
	if pw.fileHandle == nil || pw.pkgIndex != activePackageNum {
		if pw.fileHandle != nil {
			pw.fileHandle.Close()
		}
		
		currentPackagePath := fmt.Sprintf("%s/packages/%s_%d", outputDir, packageName, activePackageNum)
		flags := os.O_RDWR | os.O_CREATE | os.O_APPEND
		
		// Truncate if new package for this session
		if !createdPackages[activePackageNum] {
			flags = os.O_RDWR | os.O_CREATE | os.O_TRUNC
			createdPackages[activePackageNum] = true
		}

		f, err := os.OpenFile(currentPackagePath, flags, 0777)
		if err != nil { return err }
		pw.fileHandle = f
		pw.pkgIndex = activePackageNum
	}

	_, err := pw.fileHandle.Write(data)
	if err != nil { return err }

	// Add Frame Entry
	newEntry := evrm.Frame{
		CurrentPackageIndex: activePackageNum,
		CurrentOffset:       cEntry.CurrentOffset + cEntry.CompressedSize,
		CompressedSize:      uint32(len(data)),
		DecompressedSize:    decompressedSize,
	}
	if newEntry.CurrentOffset+newEntry.CompressedSize > math.MaxInt32 {
		newEntry.CurrentOffset = 0
	}

	manifest.Frames = append(manifest.Frames, newEntry)
	manifest.Header.Frames = incrementHeaderChunk(manifest.Header.Frames, 1)

	return nil
}

func (pw *packageWriter) close() {
	if pw.fileHandle != nil {
		pw.fileHandle.Close()
		pw.fileHandle = nil
	}
}

// --- Logic ---

func replaceFiles(fileMap [][]newFile, manifest evrm.EvrManifest) error {
	fmt.Println("Mapping modified files...")
	
	// Map construction
	totalFiles := 0
	for _, chunk := range fileMap { totalFiles += len(chunk) }
	
	modifiedFilesLookupTable := make(map[[128]byte]newFile, totalFiles)
	frameContentsLookupTable := make(map[[128]byte]evrm.FrameContents, manifest.Header.FrameContents.Count)
	modifiedFrames := make(map[uint32]bool)

	// Build Manifest Lookup
	for _, v := range manifest.FrameContents {
		buf := [128]byte{}
		binary.LittleEndian.PutUint64(buf[0:64], uint64(v.T))
		binary.LittleEndian.PutUint64(buf[64:128], uint64(v.FileSymbol))
		frameContentsLookupTable[buf] = v
	}
	
	// Build Modified Lookup
	for _, fileGroup := range fileMap {
		for _, v := range fileGroup {
			buf := [128]byte{}
			binary.LittleEndian.PutUint64(buf[0:64], uint64(v.TypeSymbol))
			binary.LittleEndian.PutUint64(buf[64:128], uint64(v.FileSymbol))
			
			if content, ok := frameContentsLookupTable[buf]; ok {
				modifiedFrames[content.FileIndex] = true
				modifiedFilesLookupTable[buf] = v
			}
		}
	}
	fmt.Printf("Mapped %d files to modify.\n", len(modifiedFilesLookupTable))

	// Group Contents
	contentsByFrame := make(map[uint32][]fcWrapper)
	for k, v := range manifest.FrameContents {
		contentsByFrame[v.FileIndex] = append(contentsByFrame[v.FileIndex], fcWrapper{index: k, fc: v})
	}

	// Prepare New Manifest
	newManifest := manifest
	newManifest.Frames = make([]evrm.Frame, 0)
	origFramesHeader := manifest.Header.Frames
	newManifest.Header.PackageCount = 1 
	newManifest.Header.Frames = evrm.HeaderChunk{
		SectionSize: 0, 
		Unk1: origFramesHeader.Unk1, 
		Unk2: origFramesHeader.Unk2, 
		ElementSize: 16, 
		Count: 0, 
		ElementCount: 0,
	}

	// Parallel Processing
	packages := make(map[uint32]*os.File)
	for i := 0; i < int(manifest.Header.PackageCount); i++ {
		pFilePath := fmt.Sprintf("%s/packages/%s_%d", dataDir, packageName, i)
		f, err := os.Open(pFilePath)
		if err != nil { return fmt.Errorf("failed to open package %s: %v", pFilePath, err) }
		packages[uint32(i)] = f
		defer f.Close()
	}

	totalFrames := int(manifest.Header.Frames.Count)
	// Increased lookahead for smoother disk IO saturation
	lookaheadSize := runtime.NumCPU() * 16
	futureResults := make(chan chan frameResult, lookaheadSize)
	writer := &packageWriter{}
	defer writer.close()

	logTimer := make(chan bool, 1)
	go logTimerFunc(logTimer)

	// 1. Dispatcher
	go func() {
		defer close(futureResults)
		for i := 0; i < totalFrames; i++ {
			resultChan := make(chan frameResult, 1)
			futureResults <- resultChan

			go func(idx int, ch chan frameResult) {
				v := manifest.Frames[idx]
				isMod := modifiedFrames[uint32(idx)]
				res := frameResult{index: idx, isModified: isMod, decompressedSize: v.DecompressedSize}

				// 1. Recycle: Get Read Buffer
				rawReadBuf := readPool.Get().([]byte)
				if cap(rawReadBuf) < int(v.CompressedSize) {
					rawReadBuf = make([]byte, int(v.CompressedSize))
				} else {
					rawReadBuf = rawReadBuf[:v.CompressedSize]
				}
				res.rawReadBuf = rawReadBuf // Store ref to return later

				// 2. Read from Disk
				activeFile := packages[v.CurrentPackageIndex]
				if v.CompressedSize > 0 {
					if _, err := activeFile.ReadAt(rawReadBuf, int64(v.CurrentOffset)); err != nil {
						// Skip marker frames on read failure
						if v.DecompressedSize == 0 {
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
					res.data = rawReadBuf // Pass through raw compressed data
					ch <- res
					return
				}

				// 3. Decompress (Using Pool)
				decompBuf := decompPool.Get().([]byte)
				// Decompress Append
				decompBytes, err := decoder.DecodeAll(rawReadBuf, decompBuf[:0])
				if err != nil {
					res.err = err
					ch <- res
					return
				}
				res.decompBuf = decompBytes // Store ref to return later (note: decompBytes points to same backing array as decompBuf)

				// 4. Construction (Using Pool)
				bufObj := constructionPool.Get()
				constructionBuf := bufObj.(*bytes.Buffer)
				constructionBuf.Reset()
				defer constructionPool.Put(bufObj) // Return immediately after encoding

				sorted := make([]fcWrapper, 0)
				if contents, ok := contentsByFrame[uint32(idx)]; ok {
					sorted = append(sorted, contents...)
				}
				sort.Slice(sorted, func(a, b int) bool {
					return sorted[a].fc.DataOffset < sorted[b].fc.DataOffset
				})

				for j := 0; j < len(sorted); j++ {
					buf := [128]byte{}
					binary.LittleEndian.PutUint64(buf[0:64], uint64(sorted[j].fc.T))
					binary.LittleEndian.PutUint64(buf[64:128], uint64(sorted[j].fc.FileSymbol))

					if modFile, exists := modifiedFilesLookupTable[buf]; exists && modFile.FileSymbol != 0 {
						modData, err := os.ReadFile(modFile.ModifiedFilePath)
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

				// 5. Compress Results (Using Pool)
				compBuf := compPool.Get().([]byte)
				encodedData := encoder.EncodeAll(constructionBuf.Bytes(), compBuf[:0])
				res.data = encodedData
				res.decompressedSize = uint32(constructionBuf.Len())
				
				ch <- res
			}(i, resultChan)
		}
	}()

	// 2. Collector
	fmt.Println("Starting repack...")
	for resultCh := range futureResults {
		res := <-resultCh
		if res.err != nil { return res.err }

		if len(logTimer) > 0 {
			<-logTimer
			status := "stock"
			if res.isModified { status = "modified" }
			fmt.Printf("\033[2K\rWriting %s frame %d/%d", status, res.index, totalFrames)
		}

		if res.shouldSkip {
			// Always return borrowed buffers even if skipping
			if res.rawReadBuf != nil { readPool.Put(res.rawReadBuf) }
			if res.decompBuf != nil { decompPool.Put(res.decompBuf) }
			if res.isModified && res.data != nil { compPool.Put(res.data) }
			continue
		}

		if res.isModified {
			// Update Manifest FrameContents
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
				binary.LittleEndian.PutUint64(buf[0:64], uint64(sorted[j].fc.T))
				binary.LittleEndian.PutUint64(buf[64:128], uint64(sorted[j].fc.FileSymbol))
				
				size := sorted[j].fc.Size
				if modFile, exists := modifiedFilesLookupTable[buf]; exists && modFile.FileSymbol != 0 {
					size = modFile.FileSize
				}
				
				newManifest.FrameContents[sorted[j].index] = evrm.FrameContents{
					T:             sorted[j].fc.T,
					FileSymbol:    sorted[j].fc.FileSymbol,
					FileIndex:     sorted[j].fc.FileIndex,
					DataOffset:    currentOffset,
					Size:          size,
					SomeAlignment: sorted[j].fc.SomeAlignment,
				}
				currentOffset += size
			}
		}

		// Write to disk
		if err := writer.write(&newManifest, res.data, res.decompressedSize); err != nil {
			return err
		}

		// RECYCLE BUFFERS
		// If it was stock, res.data IS res.rawReadBuf, so only put res.data back (or rawReadBuf, they are same).
		// If modified, res.rawReadBuf and res.decompBuf are effectively trash, recycle them. res.data is from compPool.
		if res.isModified {
			if res.rawReadBuf != nil { readPool.Put(res.rawReadBuf) }
			if res.decompBuf != nil { decompPool.Put(res.decompBuf) }
			if res.data != nil { compPool.Put(res.data) }
		} else {
			// stock frame: res.data points to res.rawReadBuf
			if res.data != nil { readPool.Put(res.data) }
		}
	}

	// Close writer to ensure flush and release locks
	writer.close()

	// --- WEIRDDATA FIX ---
	fmt.Println("\nRepack complete. Verifying packages...")
	actualPkgCount := uint32(0)
	for {
		path := fmt.Sprintf("%s/packages/%s_%d", outputDir, packageName, actualPkgCount)
		if _, err := os.Stat(path); err != nil {
			break
		}
		actualPkgCount++
	}
	newManifest.Header.PackageCount = actualPkgCount
	fmt.Printf("Verified %d packages created. Writing weirddata...\n", actualPkgCount)

	for i := uint32(0); i < newManifest.Header.PackageCount; i++ {
		path := fmt.Sprintf("%s/packages/%s_%d", outputDir, packageName, i)
		stats, err := os.Stat(path)
		if err != nil {
			fmt.Printf("Warning: Could not stat package %d: %v\n", i, err)
			continue
		}
		newEntry := evrm.Frame{
			CurrentPackageIndex: i,
			CurrentOffset:       uint32(stats.Size()),
			CompressedSize:      0, DecompressedSize: 0,
		}
		newManifest.Frames = append(newManifest.Frames, newEntry)
		newManifest.Header.Frames = incrementHeaderChunk(newManifest.Header.Frames, 1)
	}

	// Final dummy frame
	newManifest.Frames = append(newManifest.Frames, evrm.Frame{})
	newManifest.Header.Frames = incrementHeaderChunk(newManifest.Header.Frames, 1)

	// Write Manifest
	fmt.Printf("Writing manifest to %s/manifests/%s...\n", outputDir, packageName)
	if err := writeManifest(newManifest); err != nil {
		return err
	}
	
	fmt.Println("SUCCESS: Manifest and Packages Written.")
	return nil
}

func scanPackageFiles() ([][]newFile, error) {
	// Flexible scanner: Handles inputs with or without chunk folders (0, 1, 2...)
	filestats, _ := os.ReadDir(inputDir)
	files := make([][]newFile, len(filestats)+1) // Ensure at least 1 slot
	if len(files) == 0 { files = make([][]newFile, 1) }

	parseSymbol := func(s string) (int64, error) {
		s = strings.TrimSuffix(s, filepath.Ext(s))
		if strings.HasPrefix(s, "0x") {
			u, err := strconv.ParseUint(s[2:], 16, 64)
			return int64(u), err
		}
		return strconv.ParseInt(s, 10, 64)
	}

	err := filepath.Walk(inputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() { return nil }
		
		relPath, _ := filepath.Rel(inputDir, path)
		parts := strings.Split(filepath.ToSlash(relPath), "/")
		
		var chunkNum int64 = 0
		var typeStr, fileStr string

		// Logic to detect folder structure
		// Case A: input/0/Type/File (Length 3)
		// Case B: input/Type/File (Length 2)
		
		if len(parts) == 3 {
			if c, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
				chunkNum = c
				typeStr = parts[1]
				fileStr = parts[2]
			} else {
				// Maybe deep structure? Default to 0
				typeStr = parts[1]
				fileStr = parts[2]
			}
		} else if len(parts) == 2 {
			typeStr = parts[0]
			fileStr = parts[1]
		} else {
			return nil // Skip
		}

		typeSym, err := parseSymbol(typeStr)
		if err != nil { return nil }
		fileSym, err := parseSymbol(fileStr)
		if err != nil { return nil }

		// Grow slice if needed
		if int(chunkNum) >= len(files) {
			newFiles := make([][]newFile, int(chunkNum)+1)
			copy(newFiles, files)
			files = newFiles
		}

		files[chunkNum] = append(files[chunkNum], newFile{
			TypeSymbol: typeSym,
			FileSymbol: fileSym,
			ModifiedFilePath: path,
			FileSize: uint32(info.Size()),
		})
		return nil
	})
	return files, err
}

func extractFilesFromPackage(fullManifest evrm.EvrManifest) error {
	packages := make(map[uint32]*os.File)
	for i := 0; i < int(fullManifest.Header.PackageCount); i++ {
		pFilePath := fmt.Sprintf("%s/packages/%s_%d", dataDir, packageName, i)
		f, err := os.Open(pFilePath)
		if err != nil { return fmt.Errorf("failed to open package %s: %v", pFilePath, err) }
		packages[uint32(i)] = f
		defer f.Close()
	}

	textureTypes := map[int64]bool{
		-4707359568332879775: true, 5353709876897953952: true,
		-2094201140079393352: true, 5231972605540061417: true,
	}

	framesToProcess := make(map[uint32][]evrm.FrameContents)
	for _, content := range fullManifest.FrameContents {
		if texturesOnly {
			if _, ok := textureTypes[content.T]; !ok { continue }
		}
		framesToProcess[content.FileIndex] = append(framesToProcess[content.FileIndex], content)
	}

	type extractJob struct {
		frameIndex int
		data       []byte
	}

	numWorkers := runtime.NumCPU()
	jobs := make(chan extractJob, numWorkers*2)
	var wg sync.WaitGroup
	
	// Dir cache to reduce syscalls
	var dirCache sync.Map

	logTimer := make(chan bool, 1)
	go logTimerFunc(logTimer)

	worker := func() {
		defer wg.Done()
		
		// Per-worker decomp buffer
		myDecompBuf := decompPool.Get().([]byte)
		defer decompPool.Put(myDecompBuf)
		
		for job := range jobs {
			decompBytes, err := decoder.DecodeAll(job.data, myDecompBuf[:0])
			if err != nil { continue }

			if contents, ok := framesToProcess[uint32(job.frameIndex)]; ok {
				for _, v2 := range contents {
					fileName := fmt.Sprintf("%d", v2.FileSymbol)
					fileType := fmt.Sprintf("%d", v2.T)
					basePath := fmt.Sprintf("%s/%s", outputDir, fileType)
					if outputPreserveGroups {
						basePath = fmt.Sprintf("%s/%d/%s", outputDir, v2.FileIndex, fileType)
					}
					
					// Cache MkdirAll calls
					if _, exists := dirCache.Load(basePath); !exists {
						os.MkdirAll(basePath, 0777)
						dirCache.Store(basePath, true)
					}
					
					os.WriteFile(fmt.Sprintf("%s/%s", basePath, fileName), decompBytes[v2.DataOffset:v2.DataOffset+v2.Size], 0777)
				}
			}
			// Important: recycle input buffer
			readPool.Put(job.data)
		}
	}

	for i := 0; i < numWorkers; i++ { wg.Add(1); go worker() }

	for k, v := range fullManifest.Frames {
		if _, ok := framesToProcess[uint32(k)]; !ok { continue }
		activeFile := packages[v.CurrentPackageIndex]
		
		// Alloc read buffer from pool
		splitFile := readPool.Get().([]byte)
		if cap(splitFile) < int(v.CompressedSize) {
			splitFile = make([]byte, int(v.CompressedSize))
		} else {
			splitFile = splitFile[:v.CompressedSize]
		}
		
		if v.CompressedSize > 0 {
			activeFile.ReadAt(splitFile, int64(v.CurrentOffset))
		}
		if len(logTimer) > 0 { <-logTimer; fmt.Printf("\033[2K\rExtracting frame %d/%d", k, fullManifest.Header.Frames.Count) }
		jobs <- extractJob{frameIndex: k, data: splitFile}
	}
	close(jobs)
	wg.Wait()
	return nil
}

// --- Helpers ---

func decompressZSTD(b []byte) ([]byte, error) {
	return decoder.DecodeAll(b, nil)
}

func incrementHeaderChunk(chunk evrm.HeaderChunk, amount int) evrm.HeaderChunk {
	for i := 0; i < amount; i++ {
		chunk.Count++
		chunk.ElementCount++
		chunk.SectionSize += uint64(chunk.ElementSize)
	}
	return chunk
}

func writeManifest(manifest evrm.EvrManifest) error {
	os.MkdirAll(outputDir+"/manifests/", 0777)
	file, err := os.OpenFile(outputDir+"/manifests/"+packageName, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0777)
	if err != nil { return err }
	defer file.Close()
	
	manifestBytes, err := evrm.UnmarshalManifest(manifest, manifestType)
	if err != nil { return err }
	
	_, err = file.Write(compressManifest(manifestBytes))
	return err
}

func compressManifest(b []byte) []byte {
	zstdBytes := encoder.EncodeAll(b, nil)
	cHeader := CompressedHeader{
		[4]byte{0x5A, 0x53, 0x54, 0x44},
		uint32(binary.Size(CompressedHeader{})),
		uint64(len(b)),
		uint64(len(zstdBytes)),
	}
	fBuf := bytes.NewBuffer(nil)
	binary.Write(fBuf, binary.LittleEndian, cHeader)
	fBuf.Write(zstdBytes)
	return fBuf.Bytes()
}

func rebuildPackageManifestCombo(fileMap [][]newFile) error {
	return fmt.Errorf("build mode not fully refactored in this version")
}

func logTimerFunc(logTimer chan bool) {
	for {
		time.Sleep(1 * time.Second)
		logTimer <- true
	}
}
