//go:build unix

package awdb

import (
	"io"
	"os"
	"syscall"
)

// mapFile memory maps the file so that tree walks need no system calls.
//
// When mapping is not possible (for example the file lives on a filesystem
// that does not support it) the *os.File is returned as io.ReaderAt instead:
// every access then costs one pread, which is slower but still correct.
func mapFile(f *os.File, size int64) ([]byte, io.ReaderAt, func() error, error) {
	if size <= 0 || size > int64(maxInt()) {
		return nil, f, nil, nil
	}

	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, f, nil, nil
	}

	release := func() error {
		return syscall.Munmap(data)
	}
	return data, nil, release, nil
}

// maxInt is the largest value an int can hold on this platform.
func maxInt() int {
	return int(^uint(0) >> 1)
}