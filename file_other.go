//go:build !unix

package awdb

import (
	"io"
	"os"
)

// mapFile loads the file into memory on platforms without mmap support.
func mapFile(f *os.File, size int64) ([]byte, io.ReaderAt, func() error, error) {
	if size <= 0 {
		return nil, f, nil, nil
	}

	data, err := io.ReadAll(io.NewSectionReader(f, 0, size))
	if err != nil {
		return nil, nil, nil, err
	}
	return data, nil, nil, nil
}