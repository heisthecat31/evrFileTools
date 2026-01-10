package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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

type fileGroup struct {
	currentData      bytes.Buffer
	decompressedSize uint32
	fileIndex        uint32
	fileCount        int
}

type fcWrapper struct {
	index int
	fc    evrm.FrameContents
}

// Result struct for parallel processing
type frameResult struct {
	index            int
	data             []byte
	err              error
	decompressedSize uint32
	compressedSize   uint32
	isModified       bool
}

var (
	encoder *zstd.Encoder
	decoder *zstd.Decoder
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
)

func init() {
	flag.StringVar(&mode, "mode", "", "must be one of the following: 'extract', 'build', 'replace', 'jsonmanifest'")
	flag.StringVar(&manifestType, "manifestType", "5932408047-EVR", "See readme for updated list of manifest types.")
	flag.StringVar(&packageName, "packageName", "package", "File name of package, e.g. 48037dc70b0ecab2, 2b47aab238f60515, etc.")
	flag.StringVar(&dataDir, "dataDir", "", "Path of directory containing 'manifests' & 'packages' in ready-at-dawn-echo-arena/_data")
	flag.StringVar(&inputDir, "inputDir", "", "Path of directory containing modified files (same structure as '-mode extract' output)")
	flag.StringVar(&outputDir, "outputDir", "", "Path of directory to place modified package & manifest files")
	flag.BoolVar(&outputPreserveGroups, "outputPreserveGroups", false, "If true, preserve groups during '-mode extract', e.g. './output/1.../fileType/fileSymbol' instead of './output/fileType/fileSymbol'")
	flag.BoolVar(&texturesOnly, "texturesonly", false, "If true, only extracts specific texture folders (-4707359568332879775, 5353709876897953952, -2094201140079393352, 5231972605540061417)")
	flag.BoolVar(&ignoreOutputRestrictions, "ignoreOutputRestrictions", false, "Allows non-empty outputDir to be used.")
	flag.BoolVar(&help, "help", false, "Print usage")
	flag.Parse()

	if help {
		flag.Usage()
		os.Exit(0)
	}

	if mode == "jsonmanifest" && dataDir == "" {
		fmt.Println("'-mode jsonmanifest' must be used in conjunction with '-dataDir'")
		os.Exit(1)
	}

	if help || len(os.Args) == 1 || mode == "" || outputDir == "" {
		flag.Usage()
		os.Exit(1)
	}

	if mode != "extract" && mode != "build" && mode != "replace" && mode != "jsonmanifest" {
		fmt.Println("mode must be one of the following: 'extract', 'build', 'replace', 'jsonmanifest'")
		flag.Usage()
		os.Exit(1)
	}

	if mode == "build" && inputDir == "" {
		fmt.Println("'-mode build' must be used in conjunction with '-inputDir'")
		flag.Usage()
		os.Exit(1)
	}

	os.MkdirAll(outputDir, 0777)

	isOutputDirEmpty := func() bool {
		f, err := os.Open(outputDir)
		if err != nil {
			return false
		}
		defer f.Close()
		_, err = f.Readdir(1)
		return err == io.EOF
	}()

	if !isOutputDirEmpty && !ignoreOutputRestrictions {
		fmt.Println("Output directory is not empty. Use '-ignoreOutputRestrictions' to override this restriction.")
		os.Exit(1)
	}

	var err error
	decoder, err = zstd.NewReader(nil)
	if err != nil {
		panic(err)
	}
	encoder, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(compressionLevel))
	if err != nil {
		panic(err)
	}
}

func main() {
	if mode == "build" {
		fmt.Println("Building list of files to package...")
		files, err := scanPackageFiles()
		if err != nil {
			fmt.Printf("failed to scan %s", inputDir)
			panic(err)
		}

		if err := rebuildPackageManifestCombo(files); err != nil {
			fmt.Println(err)
			return
		}
		return
	}

	b, err := os.ReadFile(dataDir + "/manifests/" + packageName)
	if err != nil {
		fmt.Println("Failed to open manifest file, check dataDir path")
		return
	}

	compHeader := CompressedHeader{}
	decompBytes, err := decompressZSTD(b[binary.Size(compHeader):])
	if err != nil {
		fmt.Println("Failed to decompress manifest")
		fmt.Println(hex.Dump(b[binary.Size(compHeader):][:256]))
		fmt.Println(err)
		return
	}

	buf := bytes.NewReader(b)
	err = binary.Read(buf, binary.LittleEndian, &compHeader)
	if err != nil {
		fmt.Println("failed to marshal manifest into struct")
		return
	}

	if len(b[binary.Size(compHeader):]) != int(compHeader.CompressedSize) || len(decompBytes) != int(compHeader.UncompressedSize) {
		fmt.Println("Manifest header does not match actual size of manifest")
		return
	}

	manifest, err := evrm.MarshalManifest(decompBytes, manifestType)
	if err != nil {
		fmt.Println("Error creating manifest: ", err)
		panic(err)
	}

	if mode == "extract" {
		if err := extractFilesFromPackage(manifest); err != nil {
			fmt.Println("Error extracting files: ", err)
		}
		return
	} else if mode == "replace" {
		files, err := scanPackageFiles()
		if err != nil {
			fmt.Printf("failed to scan %s", inputDir)
			panic(err)
		}

		if err := replaceFiles(files, manifest); err != nil {
			fmt.Println(err)
			return
		}

	} else if mode == "jsonmanifest" {
		jFile, err := os.OpenFile("manifestdebug.json", os.O_RDWR|os.O_CREATE, 0777)
		if err != nil {
			return
		}
		jBytes, _ := json.MarshalIndent(manifest, "", " ")
		jFile.Write(jBytes)
		jFile.Close()
	}
}

func replaceFiles(fileMap [][]newFile, manifest evrm.EvrManifest) error {
	modifiedFrames := make(map[uint32]bool, manifest.Header.Frames.Count)
	frameContentsLookupTable := make(map[[128]byte]evrm.FrameContents, manifest.Header.FrameContents.Count)
	modifiedFilesLookupTable := make(map[[128]byte]newFile, len(fileMap[0]))

	for _, v := range manifest.FrameContents {
		buf := [128]byte{}
		binary.LittleEndian.PutUint64(buf[0:64], uint64(v.T))
		binary.LittleEndian.PutUint64(buf[64:128], uint64(v.FileSymbol))
		frameContentsLookupTable[buf] = v
	}
	for _, v := range fileMap[0] {
		buf := [128]byte{}
		binary.LittleEndian.PutUint64(buf[0:64], uint64(v.TypeSymbol))
		binary.LittleEndian.PutUint64(buf[64:128], uint64(v.FileSymbol))
		modifiedFrames[frameContentsLookupTable[buf].FileIndex] = true
		modifiedFilesLookupTable[buf] = v
	}

	// Lookup map for fast access
	contentsByFrame := make(map[uint32][]fcWrapper)
	for k, v := range manifest.FrameContents {
		contentsByFrame[v.FileIndex] = append(contentsByFrame[v.FileIndex], fcWrapper{index: k, fc: v})
	}

	// Open source packages
	packages := make(map[uint32]*os.File)
	for i := 0; i < int(manifest.Header.PackageCount); i++ {
		pFilePath := fmt.Sprintf("%s/packages/%s_%d", dataDir, packageName, i)
		f, err := os.Open(pFilePath)
		if err != nil {
			fmt.Printf("failed to open package %s\n", pFilePath)
			return err
		}
		packages[uint32(i)] = f
		defer f.Close()
	}

	newManifest := manifest
	newManifest.Frames = make([]evrm.Frame, 0)
	newManifest.Header.Frames = evrm.HeaderChunk{SectionSize: 0, Unk1: 0, Unk2: 0, ElementSize: 16, Count: 0, ElementCount: 0}

	logTimer := make(chan bool, 1)
	go logTimerFunc(logTimer)

	// --- PARALLEL PROCESSING PIPELINE ---
	totalFrames := int(manifest.Header.Frames.Count)
	
	// Channel to deliver results in strict order 0, 1, 2...
	// The buffer size determines how far "ahead" we can process
	lookaheadSize := runtime.NumCPU() * 4
	futureResults := make(chan chan frameResult, lookaheadSize)

	// 1. Dispatcher: Spawns work
	go func() {
		defer close(futureResults)

		for i := 0; i < totalFrames; i++ {
			resultChan := make(chan frameResult, 1)
			futureResults <- resultChan // Push future to ordered queue

			// Spawn worker for this specific frame
			go func(idx int, ch chan frameResult) {
				v := manifest.Frames[idx]
				
				// Read compressed data from source
				// NOTE: We need a mutex here if we share file handles, OR we can reopen/pread.
				// Since we have a map of file handles, Pread (ReadAt) is thread-safe on *nix but Windows file pointers are tricky.
				// To be safe and simple: We will read in the goroutine but use a mutex for the file access.
				// Actually, for speed, let's just read inside the worker using ReadAt (safe) 
				// BUT os.File in Go on Windows might share the seek pointer.
				// So we will just read the raw bytes in the main thread (Dispatcher) because I/O is fast, compression is slow.
				
				// Wait, the dispatcher is single threaded here. 
				// We should read the data here in the dispatcher to ensure sequential file access (fastest on HDD)
				// then send the data to the worker.
				
				activeFile := packages[v.CurrentPackageIndex]
				splitFile := make([]byte, v.CompressedSize)
				
				// Using ReadAt is thread-safe on os.File
				if v.CompressedSize > 0 {
					_, err := activeFile.ReadAt(splitFile, int64(v.CurrentOffset))
					if err != nil {
						ch <- frameResult{index: idx, err: err}
						return
					}
				}

				// Processing Logic
				isMod := modifiedFrames[uint32(idx)]
				res := frameResult{index: idx, data: splitFile, decompressedSize: v.DecompressedSize, isModified: isMod}

				if !isMod {
					// Unmodified: Just return the raw data
					ch <- res
					return
				}

				// Modified: Decompress -> Patch -> Compress
				decompBytes, err := decompressZSTD(splitFile)
				if err != nil {
					res.err = err
					ch <- res
					return
				}

				sortedFrameContents := make([]fcWrapper, 0)
				if contents, ok := contentsByFrame[uint32(idx)]; ok {
					sortedFrameContents = append(sortedFrameContents, contents...)
				}

				sort.Slice(sortedFrameContents, func(a, b int) bool {
					return sortedFrameContents[a].fc.DataOffset < sortedFrameContents[b].fc.DataOffset
				})

				constructedFile := bytes.NewBuffer(make([]byte, 0, v.DecompressedSize))
				
				for j := 0; j < len(sortedFrameContents); j++ {
					buf := [128]byte{}
					binary.LittleEndian.PutUint64(buf[0:64], uint64(sortedFrameContents[j].fc.T))
					binary.LittleEndian.PutUint64(buf[64:128], uint64(sortedFrameContents[j].fc.FileSymbol))
					
					if modFile, exists := modifiedFilesLookupTable[buf]; exists && modFile.FileSymbol != 0 {
						// Load replacement file
						modData, err := os.ReadFile(modFile.ModifiedFilePath)
						if err != nil {
							res.err = err
							ch <- res
							return
						}
						
						// Update Manifest info in MAIN thread later, but store needed data here if complex?
						// Actually we modify manifest in the Writer loop. 
						// But we need to update the FrameContents in manifest for the size change.
						// This struct is shared memory. We must not write to 'manifest' here.
						// We will perform the file reads here (slow I/O) and writing to buffer.
						
						constructedFile.Write(modData)
					} else {
						// Original file
						start := sortedFrameContents[j].fc.DataOffset
						end := start + sortedFrameContents[j].fc.Size
						constructedFile.Write(decompBytes[start:end])
					}
				}

				// Compress result
				res.data = encoder.EncodeAll(constructedFile.Bytes(), nil)
				res.decompressedSize = uint32(constructedFile.Len())
				
				ch <- res
			}(i, resultChan)
		}
	}()

	// 2. Collector: Receives results in strict order and writes to disk
	for resultCh := range futureResults {
		res := <-resultCh // Wait for the specific next frame
		if res.err != nil {
			return res.err
		}

		// Update UI/Log
		if len(logTimer) > 0 {
			<-logTimer
			status := "stock"
			if res.isModified {
				status = "modified"
			}
			fmt.Printf("\033[2K\rWriting %s frame %d/%d", status, res.index, totalFrames)
		}

		// Update Manifest & Frame Contents
		// NOTE: For modified files, we need to update the FrameContents list with new offsets/sizes.
		// Since we did the construction inside the worker, we need to replicate the 'size' updates here 
		// or pass them back. 
		// Actually, to ensure accuracy without complex message passing, we can just update the FrameContents logic
		// for 'modified' frames.
		
		if res.isModified {
			// We need to update the manifest FrameContents sizes/offsets for this frame.
			// Re-calculating offsets based on the files we know we put in.
			
			sortedFrameContents := make([]fcWrapper, 0)
			if contents, ok := contentsByFrame[uint32(res.index)]; ok {
				sortedFrameContents = append(sortedFrameContents, contents...)
			}
			sort.Slice(sortedFrameContents, func(a, b int) bool {
				return sortedFrameContents[a].fc.DataOffset < sortedFrameContents[b].fc.DataOffset
			})

			currentOffset := uint32(0)
			for j := 0; j < len(sortedFrameContents); j++ {
				buf := [128]byte{}
				binary.LittleEndian.PutUint64(buf[0:64], uint64(sortedFrameContents[j].fc.T))
				binary.LittleEndian.PutUint64(buf[64:128], uint64(sortedFrameContents[j].fc.FileSymbol))
				
				size := sortedFrameContents[j].fc.Size // Default original size
				
				if modFile, exists := modifiedFilesLookupTable[buf]; exists && modFile.FileSymbol != 0 {
					// We need the size of the file we just embedded.
					// Since we don't want to re-read disk, we rely on ScanPackageFiles having populated FileSize?
					// Yes, newFile struct has FileSize.
					size = modFile.FileSize
				}
				
				// Update Manifest
				newManifest.FrameContents[sortedFrameContents[j].index] = evrm.FrameContents{
					T:             sortedFrameContents[j].fc.T,
					FileSymbol:    sortedFrameContents[j].fc.FileSymbol,
					FileIndex:     sortedFrameContents[j].fc.FileIndex,
					DataOffset:    currentOffset,
					Size:          size,
					SomeAlignment: sortedFrameContents[j].fc.SomeAlignment,
				}
				currentOffset += size
			}
		}

		// Append to package
		err := appendChunkToPackages(&newManifest, fileGroup{
			currentData: *bytes.NewBuffer(res.data), 
			decompressedSize: res.decompressedSize, // Set this to trigger 'already compressed' path
		})
		if err != nil {
			return err
		}
	}

	// weirddata
	for i := uint32(0); i < newManifest.Header.PackageCount; i++ {
		packageStats, err := os.Stat(fmt.Sprintf("%s/packages/%s_%d", outputDir, packageName, i))
		if err != nil {
			fmt.Println("failed to stat package for weirddata writing")
			return err
		}
		newEntry := evrm.Frame{
			CurrentPackageIndex: i,
			CurrentOffset:       uint32(packageStats.Size()),
			CompressedSize:      0,
			DecompressedSize:    0,
		}
		newManifest.Frames = append(newManifest.Frames, newEntry)
		newManifest.Header.Frames = incrementHeaderChunk(newManifest.Header.Frames, 1)
	}

	newEntry := evrm.Frame{}
	newManifest.Frames = append(newManifest.Frames, newEntry)
	newManifest.Header.Frames = incrementHeaderChunk(newManifest.Header.Frames, 1)

	// write new manifest
	err := writeManifest(newManifest)
	if err != nil {
		return err
	}

	fmt.Printf("\nfinished, modified %d files\n", len(modifiedFilesLookupTable))
	return nil
}

func decompressZSTD(b []byte) ([]byte, error) {
	return decoder.DecodeAll(b, nil)
}

func rebuildPackageManifestCombo(fileMap [][]newFile) error {
	totalFileCount := 0
	for _, v := range fileMap {
		totalFileCount += len(v)
	}
	fmt.Printf("Building from %d files\n", totalFileCount)
	manifest := evrm.EvrManifest{
		Header: evrm.ManifestHeader{
			PackageCount:  1,
			Unk1:          0,
			Unk2:          0,
			FrameContents: evrm.HeaderChunk{SectionSize: 0, Unk1: 0, Unk2: 0, ElementSize: 32, Count: 0, ElementCount: 0},
			SomeStructure: evrm.HeaderChunk{SectionSize: 0, Unk1: 0, Unk2: 0, ElementSize: 40, Count: 0, ElementCount: 0},
			Frames:        evrm.HeaderChunk{SectionSize: 0, Unk1: 0, Unk2: 0, ElementSize: 16, Count: 0, ElementCount: 0},
		},
		FrameContents: make([]evrm.FrameContents, totalFileCount),
		SomeStructure: make([]evrm.SomeStructure, totalFileCount),
		Frames:        []evrm.Frame{},
	}

	currentFileGroup := fileGroup{}
	totalFilesWritten := 0

	logTimer := make(chan bool, 1)
	go logTimerFunc(logTimer)

	for _, files := range fileMap {
		if currentFileGroup.currentData.Len() != 0 {
			if err := appendChunkToPackages(&manifest, currentFileGroup); err != nil {
				return err
			}
			currentFileGroup.currentData.Reset()
			currentFileGroup.fileIndex++
			currentFileGroup.fileCount = 0
		}
		for _, file := range files {
			toWrite, err := os.ReadFile(file.ModifiedFilePath)
			if err != nil {
				return err
			}

			frameContentsEntry := evrm.FrameContents{
				T:             file.TypeSymbol,
				FileSymbol:    file.FileSymbol,
				FileIndex:     currentFileGroup.fileIndex,
				DataOffset:    uint32(currentFileGroup.currentData.Len()),
				Size:          uint32(len(toWrite)),
				SomeAlignment: 1,
			}
			someStructureEntry := evrm.SomeStructure{
				T:          file.TypeSymbol,
				FileSymbol: file.FileSymbol,
				Unk1:       0,
				Unk2:       0,
				AssetType:  0,
			}

			manifest.FrameContents[totalFilesWritten] = frameContentsEntry
			manifest.SomeStructure[totalFilesWritten] = someStructureEntry
			manifest.Header.FrameContents = incrementHeaderChunk(manifest.Header.FrameContents, 1)
			manifest.Header.SomeStructure = incrementHeaderChunk(manifest.Header.SomeStructure, 1)

			totalFilesWritten++
			currentFileGroup.fileCount++
			currentFileGroup.currentData.Write(toWrite)
		}
		if len(logTimer) > 0 {
			<-logTimer
			fmt.Printf("\033[2K\rWrote %d/%d files ", totalFilesWritten, totalFileCount)
		}
	}
	if currentFileGroup.currentData.Len() > 0 {
		if err := appendChunkToPackages(&manifest, currentFileGroup); err != nil {
			return err
		}
		currentFileGroup.currentData.Reset()
		currentFileGroup.fileIndex++
		currentFileGroup.fileCount = 0
	}
	fmt.Printf("finished writing package data, %d files in %d packages\n", totalFilesWritten, manifest.Header.PackageCount)

	for i := uint32(0); i < manifest.Header.PackageCount; i++ {
		packageStats, err := os.Stat(fmt.Sprintf("%s/packages/%s_%d", outputDir, packageName, i))
		if err != nil {
			fmt.Println("failed to stat package for weirddata writing")
			return err
		}
		newEntry := evrm.Frame{
			CurrentPackageIndex: i,
			CurrentOffset:       uint32(packageStats.Size()),
			CompressedSize:      0,
			DecompressedSize:    0,
		}
		manifest.Frames = append(manifest.Frames, newEntry)
		manifest.Header.Frames = incrementHeaderChunk(manifest.Header.Frames, 1)
	}

	newEntry := evrm.Frame{}
	manifest.Frames = append(manifest.Frames, newEntry)
	manifest.Header.Frames = incrementHeaderChunk(manifest.Header.Frames, 1)

	fmt.Println("Writing manifest")
	if err := writeManifest(manifest); err != nil {
		return err
	}
	return nil
}

func appendChunkToPackages(manifest *evrm.EvrManifest, currentFileGroup fileGroup) error {
	os.MkdirAll(fmt.Sprintf("%s/packages", outputDir), 0777)

	cEntry := evrm.Frame{}
	activePackageNum := uint32(0)
	if len(manifest.Frames) > 0 {
		cEntry = manifest.Frames[len(manifest.Frames)-1]
		activePackageNum = cEntry.CurrentPackageIndex
	}
	var compFile []byte
	var err error
	
	// If decompressedSize is set, it means data is ALREADY compressed (from parallel worker or build func)
	// We use the 'decompressedSize' field in fileGroup as a flag/carrier for the real size, 
	// while 'currentData' holds the COMPRESSED bytes.
	if currentFileGroup.decompressedSize != 0 {
		compFile = currentFileGroup.currentData.Bytes()
	} else {
		compFile = encoder.EncodeAll(currentFileGroup.currentData.Bytes(), nil)
	}

	currentPackagePath := fmt.Sprintf("%s/packages/%s_%d", outputDir, packageName, activePackageNum)

	if int(cEntry.CurrentOffset+cEntry.CompressedSize)+len(compFile) > math.MaxInt32 {
		activePackageNum++
		manifest.Header.PackageCount = activePackageNum + 1
		currentPackagePath = fmt.Sprintf("%s/packages/%s_%d", outputDir, packageName, activePackageNum)
	}

	f, err := os.OpenFile(currentPackagePath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0777)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(compFile)
	if err != nil {
		return err
	}

	newEntry := evrm.Frame{
		CurrentPackageIndex: activePackageNum,
		CurrentOffset:       cEntry.CurrentOffset + cEntry.CompressedSize,
		CompressedSize:      uint32(len(compFile)),
		// If it was pre-compressed, we used decompressedSize to pass the real decompressed size.
		// If it was just compressed here, we used currentData.Len() as decompressed size.
		DecompressedSize:    uint32(currentFileGroup.currentData.Len()),
	}
	
	if currentFileGroup.decompressedSize != 0 {
		newEntry.DecompressedSize = currentFileGroup.decompressedSize
	}

	if newEntry.CurrentOffset+newEntry.CompressedSize > math.MaxInt32 {
		newEntry.CurrentOffset = 0
	}

	manifest.Frames = append(manifest.Frames, newEntry)
	manifest.Header.Frames = incrementHeaderChunk(manifest.Header.Frames, 1)

	return nil
}

func scanPackageFiles() ([][]newFile, error) {
	parseSymbol := func(s string) (int64, error) {
		if ext := filepath.Ext(s); ext != "" {
			s = s[:len(s)-len(ext)]
		}

		if strings.HasPrefix(s, "0x") {
			u, err := strconv.ParseUint(s[2:], 16, 64)
			return int64(u), err
		}
		return strconv.ParseInt(s, 10, 64)
	}

	filestats, _ := os.ReadDir(inputDir)
	files := make([][]newFile, len(filestats))
	err := filepath.Walk(inputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Println(err)
			return err
		}
		if info.IsDir() {
			return nil
		}
		scannedFile := newFile{}
		scannedFile.ModifiedFilePath = path
		scannedFile.FileSize = uint32(info.Size())
		
		path = filepath.ToSlash(path)
		foo := strings.Split(path, "/")
		
		if len(foo) < 3 {
			return nil
		}

		dir1 := foo[len(foo)-3]
		dir2 := foo[len(foo)-2]
		dir3 := foo[len(foo)-1]

		chunkNum, err := strconv.ParseInt(dir1, 10, 64)
		if err != nil {
			return nil 
		}

		typeSymbol, err := parseSymbol(dir2)
		if err != nil {
			return nil
		}
		scannedFile.TypeSymbol = typeSymbol

		fileSymbol, err := parseSymbol(dir3)
		if err != nil {
			return nil
		}
		scannedFile.FileSymbol = fileSymbol

		if int(chunkNum) >= len(files) {
			for i := len(files); i <= int(chunkNum); i++ {
				files = append(files, []newFile{})
			}
		}

		files[chunkNum] = append(files[chunkNum], scannedFile)
		return nil
	})

	if err != nil {
		return nil, err
	}
	return files, nil
}

func extractFilesFromPackage(fullManifest evrm.EvrManifest) error {
	packages := make(map[uint32]*os.File)
	totalFilesWritten := 0

	for i := 0; i < int(fullManifest.Header.PackageCount); i++ {
		pFilePath := fmt.Sprintf("%s/packages/%s_%d", dataDir, packageName, i)
		f, err := os.Open(pFilePath)
		if err != nil {
			fmt.Printf("failed to open package %s\n", pFilePath)
			return err
		}
		packages[uint32(i)] = f
		defer f.Close()
	}

	textureTypes := map[int64]bool{
		-4707359568332879775: true,
		5353709876897953952:  true,
		-2094201140079393352: true,
		5231972605540061417:  true,
	}

	framesToProcess := make(map[uint32][]evrm.FrameContents)
	for _, content := range fullManifest.FrameContents {
		if texturesOnly {
			if _, ok := textureTypes[content.T]; !ok {
				continue
			}
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

	logTimer := make(chan bool, 1)
	go logTimerFunc(logTimer)

	worker := func() {
		defer wg.Done()
		for job := range jobs {
			decompBytes, err := decompressZSTD(job.data)
			if err != nil {
				fmt.Printf("Error decompressing frame %d: %v\n", job.frameIndex, err)
				continue
			}

			if len(decompBytes) != int(fullManifest.Frames[job.frameIndex].DecompressedSize) {
				fmt.Printf("Size mismatch frame %d\n", job.frameIndex)
				continue
			}

			if contents, ok := framesToProcess[uint32(job.frameIndex)]; ok {
				for _, v2 := range contents {
					fileName := fmt.Sprintf("%d", v2.FileSymbol)
					fileType := fmt.Sprintf("%d", v2.T)

					basePath := fmt.Sprintf("%s/%s", outputDir, fileType)
					if outputPreserveGroups {
						basePath = fmt.Sprintf("%s/%d/%s", outputDir, v2.FileIndex, fileType)
					}
					os.MkdirAll(basePath, 0777)
					
					err := os.WriteFile(fmt.Sprintf("%s/%s", basePath, fileName), decompBytes[v2.DataOffset:v2.DataOffset+v2.Size], 0777)
					if err != nil {
						fmt.Println(err)
					}
				}
			}
		}
	}

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go worker()
	}

	for k, v := range fullManifest.Frames {
		if _, ok := framesToProcess[uint32(k)]; !ok {
			continue
		}

		activeFile := packages[v.CurrentPackageIndex]
		activeFile.Seek(int64(v.CurrentOffset), 0)

		splitFile := make([]byte, v.CompressedSize)
		if v.CompressedSize == 0 {
			continue
		}
		_, err := io.ReadAtLeast(activeFile, splitFile, int(v.CompressedSize))

		if err != nil && v.DecompressedSize == 0 {
			continue
		} else if err != nil {
			fmt.Println("failed to read file, check input")
			return err
		}

		if len(logTimer) > 0 {
			<-logTimer
			fmt.Printf("\033[2K\rExtracting frame %d/%d", k, fullManifest.Header.Frames.Count)
		}

		jobs <- extractJob{frameIndex: k, data: splitFile}
		totalFilesWritten++ 
	}

	close(jobs)
	wg.Wait()
	return nil
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
	file, err := os.OpenFile(outputDir+"/manifests/"+packageName, os.O_RDWR|os.O_CREATE, 0777)
	if err != nil {
		return err
	}
	manifestBytes, err := evrm.UnmarshalManifest(manifest, manifestType)
	if err != nil {
		return err
	}
	file.Write(compressManifest(manifestBytes))
	file.Close()
	return nil
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

func logTimerFunc(logTimer chan bool) {
	for {
		time.Sleep(1 * time.Second)
		logTimer <- true
	}
}
