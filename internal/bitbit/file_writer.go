package bitbit

import (
	"fmt"
	"os"
	"path/filepath"
)

type FileWriter struct {
	RootPath    string
	TotalLength int
	PieceLength int
	Segments    []fileSegment
}

type fileSegment struct {
	Path   string
	Start  int64
	Length int64
	File   *os.File
}

func NewFileWriter(torrent *TorrentFile) (*FileWriter, error) {
	fw := &FileWriter{
		RootPath:    torrent.Info.Name,
		TotalLength: torrent.Info.Length,
		PieceLength: torrent.Info.PieceLength,
	}

	if torrent.Info.IsMultiFile() {
		if err := os.MkdirAll(torrent.Info.Name, 0755); err != nil {
			return nil, fmt.Errorf("creating output dir: %w", err)
		}
	} else if err := os.MkdirAll(filepath.Dir(torrent.Info.Name), 0755); err != nil && filepath.Dir(torrent.Info.Name) != "." {
		return nil, fmt.Errorf("creating parent dir: %w", err)
	}

	var offset int64
	if torrent.Info.IsMultiFile() {
		for _, tf := range torrent.Info.Files {
			relPath := filepath.Join(tf.Path...)
			fullPath := filepath.Join(torrent.Info.Name, relPath)
			seg, err := createSegment(fullPath, offset, int64(tf.Length))
			if err != nil {
				fw.Close()
				return nil, err
			}
			fw.Segments = append(fw.Segments, seg)
			offset += int64(tf.Length)
		}
	} else {
		seg, err := createSegment(torrent.Info.Name, 0, int64(torrent.Info.Length))
		if err != nil {
			return nil, err
		}
		fw.Segments = append(fw.Segments, seg)
	}

	return fw, nil
}

func createSegment(path string, start, length int64) (fileSegment, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil && filepath.Dir(path) != "." {
		return fileSegment{}, fmt.Errorf("creating dir for %s: %w", path, err)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fileSegment{}, fmt.Errorf("creating output file %s: %w", path, err)
	}
	if err := f.Truncate(length); err != nil {
		f.Close()
		return fileSegment{}, fmt.Errorf("pre-allocating %s: %w", path, err)
	}

	return fileSegment{
		Path:   path,
		Start:  start,
		Length: length,
		File:   f,
	}, nil
}

func (fw *FileWriter) WritePiece(index int, data []byte) error {
	offset := int64(index) * int64(fw.PieceLength)

	if offset+int64(len(data)) > int64(fw.TotalLength) {
		return fmt.Errorf("piece %d write would exceed file bounds", index)
	}

	return fw.WriteAt(offset, data)
}

func (fw *FileWriter) WriteAt(offset int64, data []byte) error {
	if len(data) == 0 {
		return nil
	}

	end := offset + int64(len(data))
	for _, seg := range fw.Segments {
		segEnd := seg.Start + seg.Length
		if end <= seg.Start || offset >= segEnd {
			continue
		}

		writeStart := maxInt64(offset, seg.Start)
		writeEnd := minInt64(end, segEnd)
		chunk := data[writeStart-offset : writeEnd-offset]
		if _, err := seg.File.WriteAt(chunk, writeStart-seg.Start); err != nil {
			return fmt.Errorf("writing %s at offset %d: %w", seg.Path, writeStart-seg.Start, err)
		}
	}

	return nil
}

func (fw *FileWriter) Close() error {
	var firstErr error
	for _, seg := range fw.Segments {
		if seg.File == nil {
			continue
		}
		if err := seg.File.Sync(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("syncing %s: %w", seg.Path, err)
		}
		if err := seg.File.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("closing %s: %w", seg.Path, err)
		}
	}
	return firstErr
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
