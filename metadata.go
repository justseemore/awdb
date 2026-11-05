package awdb

import (
	"encoding/json"
	"fmt"
)

// IP version values used in the header.
const (
	IPVersion4  = "4"   // IPv4 only
	IPVersion6  = "6"   // IPv6 only
	IPVersion46 = "4_6" // one tree serving both families
)

// Decode types used in the header.
const (
	// DecodeStructure records are self describing: every value carries a type
	// tag, so strings, numbers, arrays and shared pointers can be stored.
	DecodeStructure = 1

	// DecodeDirect records are a single tab separated line of text.
	DecodeDirect = 2
)

// Metadata is the JSON header stored at the beginning of an AWDB file.
//
// The values are read once when the file is opened and never mutated, so the
// returned copy can be shared between goroutines.
type Metadata struct {
	// NodeCount is the number of nodes in the search tree.
	NodeCount uint32

	// IPVersion is one of IPVersion4, IPVersion6 or IPVersion46.
	IPVersion string

	// DecodeType is DecodeStructure or DecodeDirect.
	DecodeType int

	// ByteLen is the width in bytes of every child pointer in the tree
	// (2, 3 or 4 in practice).
	ByteLen int

	// Columns holds the scalar column names, in stored order.
	Columns []string

	// MultiArea describes the optional trailing list-of-rows column, for
	// example a province -> [districts] table. It is nil for flat databases.
	MultiArea *MultiArea
}

// MultiArea describes the last column of a database that stores a list of
// sub records (for example a list of "code / name" pairs).
type MultiArea struct {
	// Name is the column name of the list itself.
	Name string `json:"name"`

	// Keys are the column names of every row of the list.
	Keys []string `json:"keys"`
}

// rawMetadata mirrors the JSON header on the wire.
type rawMetadata struct {
	NodeCount  uint32            `json:"node_count"`
	IPVersion  json.RawMessage   `json:"ip_version"`
	DecodeType int               `json:"decode_type"`
	ByteLen    int               `json:"byte_len"`
	Columns    []json.RawMessage `json:"columns"`
}

// parseMetadata decodes and validates the JSON header.
func parseMetadata(raw []byte) (Metadata, error) {
	var rm rawMetadata
	if err := json.Unmarshal(raw, &rm); err != nil {
		return Metadata{}, fmt.Errorf("%w: invalid JSON header: %v", ErrCorrupt, err)
	}

	version, err := parseIPVersion(rm.IPVersion)
	if err != nil {
		return Metadata{}, err
	}
	if rm.ByteLen < 1 || rm.ByteLen > 8 {
		return Metadata{}, fmt.Errorf("%w: byte_len %d is outside 1..8", ErrCorrupt, rm.ByteLen)
	}
	if rm.DecodeType != DecodeStructure && rm.DecodeType != DecodeDirect {
		return Metadata{}, fmt.Errorf("%w: decode_type %d", ErrUnsupported, rm.DecodeType)
	}

	columns, multi, err := parseColumns(rm.Columns)
	if err != nil {
		return Metadata{}, err
	}
	if len(columns) == 0 && multi == nil {
		return Metadata{}, fmt.Errorf("%w: header defines no columns", ErrCorrupt)
	}

	return Metadata{
		NodeCount:  rm.NodeCount,
		IPVersion:  version,
		DecodeType: rm.DecodeType,
		ByteLen:    rm.ByteLen,
		Columns:    columns,
		MultiArea:  multi,
	}, nil
}

// parseIPVersion accepts both "4" / "6" / "4_6" and the numbers 4 / 6.
func parseIPVersion(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("%w: header has no ip_version", ErrCorrupt)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case IPVersion4, IPVersion6, IPVersion46:
			return s, nil
		}
		return "", fmt.Errorf("%w: ip_version %q", ErrUnsupported, s)
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		switch n {
		case 4:
			return IPVersion4, nil
		case 6:
			return IPVersion6, nil
		}
		return "", fmt.Errorf("%w: ip_version %d", ErrUnsupported, n)
	}
	return "", fmt.Errorf("%w: ip_version is neither a string nor an integer", ErrCorrupt)
}

// parseColumns splits the column list into scalar names plus an optional
// trailing list column. The name of the list column is the scalar name
// immediately in front of it, exactly like the reference readers.
func parseColumns(raw []json.RawMessage) ([]string, *MultiArea, error) {
	columns := make([]string, 0, len(raw))
	var multi *MultiArea

	for i, item := range raw {
		var name string
		if err := json.Unmarshal(item, &name); err == nil {
			columns = append(columns, name)
			continue
		}

		var keys []string
		if err := json.Unmarshal(item, &keys); err != nil {
			return nil, nil, fmt.Errorf("%w: columns[%d] is neither a string nor a list of strings", ErrCorrupt, i)
		}
		if i != len(raw)-1 {
			return nil, nil, fmt.Errorf("%w: the list column must be the last one", ErrCorrupt)
		}
		if len(columns) == 0 {
			return nil, nil, fmt.Errorf("%w: the list column has no name", ErrCorrupt)
		}
		multi = &MultiArea{Name: columns[len(columns)-1], Keys: keys}
		columns = columns[:len(columns)-1]
	}

	return columns, multi, nil
}