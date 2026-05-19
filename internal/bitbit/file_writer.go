package bitbit

import (
	"fmt"
	"os"
)

type FileWriter struct {
	OutputPath  string
	TotalLength int
	PieceLength int
	File        *os.File
}

// This creates the output file pre-allocated to TotalLength bytes.
func NewFileWriter(path string, totalLength int, pieceLength int) (*FileWriter, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("creating output file: %w", err)
	}

	if err := f.Truncate(int64(totalLength)); err != nil {
		f.Close()
		return nil, fmt.Errorf("pre-allocating file: %w", err)
	}

	return &FileWriter{
		OutputPath:  path,
		TotalLength: totalLength,
		PieceLength: pieceLength,
		File:        f,
	}, nil
}

// This writes verified piece data at the correct byte offset.
func (fw *FileWriter) WritePiece(index int, data []byte) error {
	offset := int64(index) * int64(fw.PieceLength)

	if offset+int64(len(data)) > int64(fw.TotalLength) {
		return fmt.Errorf("piece %d write would exceed file bounds", index)
	}

	_, err := fw.File.WriteAt(data, offset)
	if err != nil {
		return fmt.Errorf("writing piece %d at offset %d: %w", index, offset, err)
	}

	return nil
}
func (fw *FileWriter) Close() error {
	if err := fw.File.Sync(); err != nil {
		return fmt.Errorf("syncing file: %w", err)
	}
	return fw.File.Close()
}
