package awdb

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// Type tags used by the "structure" record encoding (decode_type 1).
const (
	dataTypeArray      uint8 = 1
	dataTypePointer    uint8 = 2
	dataTypeString     uint8 = 3
	dataTypeLongString uint8 = 4
	dataTypeUint       uint8 = 5
	dataTypeInt        uint8 = 6
	dataTypeFloat      uint8 = 7
	dataTypeDouble     uint8 = 8
)

// decodeValue decodes one self describing value that starts at off and returns
// it together with the offset of the next value.
//
// Depth only grows when following a container or a pointer, so maxDecodeDepth
// also protects against pointer rings in corrupt files.
func (r *Reader) decodeValue(off int64, depth int) (any, int64, error) {
	if depth > maxDecodeDepth {
		return nil, 0, fmt.Errorf("%w: values nested deeper than %d levels", ErrCorrupt, maxDecodeDepth)
	}

	tag, err := r.byteAt(off)
	if err != nil {
		return nil, 0, err
	}
	off++

	switch tag {
	case dataTypeArray:
		count, err := r.byteAt(off)
		if err != nil {
			return nil, 0, err
		}
		off++
		items := make([]any, 0, count)
		for i := 0; i < int(count); i++ {
			var item any
			item, off, err = r.decodeValue(off, depth+1)
			if err != nil {
				return nil, 0, err
			}
			items = append(items, item)
		}
		return items, off, nil

	case dataTypePointer:
		size, err := r.byteAt(off)
		if err != nil {
			return nil, 0, err
		}
		off++
		raw, err := r.region(off, int(size))
		if err != nil {
			return nil, 0, err
		}
		off += int64(size)

		target := r.dataStart + int64(beUint(raw))
		if target < r.dataStart || target >= r.size {
			return nil, 0, fmt.Errorf("%w: pointer target %d is outside the data section", ErrCorrupt, target)
		}
		value, _, err := r.decodeValue(target, depth+1)
		if err != nil {
			return nil, 0, err
		}
		return value, off, nil

	case dataTypeString:
		size, err := r.byteAt(off)
		if err != nil {
			return nil, 0, err
		}
		off++
		raw, err := r.region(off, int(size))
		if err != nil {
			return nil, 0, err
		}
		off += int64(size)
		return string(raw), off, nil

	case dataTypeLongString:
		sizeBytes, err := r.byteAt(off)
		if err != nil {
			return nil, 0, err
		}
		off++
		lenBuf, err := r.region(off, int(sizeBytes))
		if err != nil {
			return nil, 0, err
		}
		off += int64(sizeBytes)

		length := beUint(lenBuf)
		if length > uint64(r.size) {
			return nil, 0, fmt.Errorf("%w: string length %d is larger than the file", ErrCorrupt, length)
		}
		raw, err := r.region(off, int(length))
		if err != nil {
			return nil, 0, err
		}
		off += int64(length)
		return string(raw), off, nil

	case dataTypeUint:
		size, err := r.byteAt(off)
		if err != nil {
			return nil, 0, err
		}
		off++
		raw, err := r.region(off, int(size))
		if err != nil {
			return nil, 0, err
		}
		off += int64(size)
		return beUint(raw), off, nil

	case dataTypeInt:
		raw, err := r.region(off, 4)
		if err != nil {
			return nil, 0, err
		}
		off += 4
		return int64(int32(binary.BigEndian.Uint32(raw))), off, nil

	case dataTypeFloat:
		raw, err := r.region(off, 4)
		if err != nil {
			return nil, 0, err
		}
		off += 4
		// Stored as a float32; widened like the reference readers do, so JSON
		// output stays identical.
		return float64(math.Float32frombits(binary.BigEndian.Uint32(raw))), off, nil

	case dataTypeDouble:
		raw, err := r.region(off, 8)
		if err != nil {
			return nil, 0, err
		}
		off += 8
		return math.Float64frombits(binary.BigEndian.Uint64(raw)), off, nil
	}

	return nil, 0, fmt.Errorf("%w: unknown data type %d at offset %d", ErrCorrupt, tag, off-1)
}

// decodeDirect decodes a "direct" record (decode_type 2): a big endian uint32
// byte count followed by a tab separated line of text.
func (r *Reader) decodeDirect(off int64) ([]any, error) {
	lenBuf, err := r.region(off, 4)
	if err != nil {
		return nil, err
	}
	size := int64(binary.BigEndian.Uint32(lenBuf))
	off += 4

	payload, err := r.region(off, int(size))
	if err != nil {
		return nil, err
	}

	parts := strings.Split(string(payload), "\t")
	items := make([]any, len(parts))
	for i, part := range parts {
		items[i] = part
	}
	return items, nil
}