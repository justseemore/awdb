// Package awdb reads IP geolocation databases in the AWDB format produced by
// www.ipplus360.com.
//
// A file is laid out as
//
//	uint16 BE   header length
//	JSON        header: node_count, ip_version, decode_type, byte_len, columns
//	tree        node_count * 2 * byte_len bytes, every child is a BE integer
//	data        records, addressed relative to the end of the tree
//
// A lookup walks one bit of the packed address per tree level (MSB first),
// then decodes the record the leaf points at. The implementation is a port of
// the official PHP reader (gitee.com/aiwen_home/awdb-php) and matches the
// official Python reader (gitee.com/aiwen_home/awdb-python) result for result.
//
// Basic use:
//
//	reader, err := awdb.Open("ip.awdb")
//	if err != nil {
//		return err
//	}
//	defer reader.Close()
//
//	record, err := reader.Lookup("114.114.114.114")
//	if errors.Is(err, awdb.ErrNotFound) {
//		// no data for this address
//	}
//	fmt.Println(record.Str("country"))
package awdb

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sync/atomic"
)

const (
	// headerSizeLen is the number of bytes of the big endian header length.
	headerSizeLen = 2

	// dataSectionBias turns a leaf value into an offset inside the data
	// section: rel = value - node_count - dataSectionBias. The offset is
	// relative to the first byte after the search tree. It is part of the
	// on-disk format, not an implementation detail.
	dataSectionBias = 10

	// maxDecodeDepth bounds the recursion of the structure decoder so that a
	// corrupt file with a pointer loop cannot overflow the stack.
	maxDecodeDepth = 32

	// ipv4BranchBit is the bit offset (and, in a "4_6" tree, the node index)
	// where the IPv4 subtree of a combined database lives.
	ipv4BranchBit = 96
)

// Reader gives random access to one AWDB file.
//
// A Reader may be used from several goroutines at the same time as long as
// Close is not called concurrently. Records returned by Lookup are immutable
// and can be shared as well.
type Reader struct {
	// src holds the whole file when it is memory mapped or was passed in by
	// the caller. When it is nil the file is read through ra.
	src  []byte
	ra   io.ReaderAt
	size int64

	meta      Metadata
	treeStart int64 // offset of the first byte of the search tree
	dataStart int64 // offset of the first byte of the data section
	nodeCount uint64
	byteLen   int

	closer func() error
	closed atomic.Bool
}

// Open opens the database at path.
//
// On unix-like systems the file is memory mapped, so lookups do not need a
// system call per tree node. If mapping is not possible the file is read with
// pread instead, which keeps the memory footprint small.
//
// Pass WithPreload(true) to read the whole file into memory instead: no mmap,
// no file descriptor, RSS grows by the file size right away.
func Open(path string, opts ...Option) (*Reader, error) {
	cfg := newOptions(opts)

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		f.Close()
		return nil, fmt.Errorf("awdb: %s is a directory", path)
	}

	// 预加载：整文件读进堆，交给 OpenBytes 持有；不保留句柄。
	if cfg.preload {
		f.Close()
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return OpenBytes(data)
	}

	src, ra, release, err := mapFile(f, info.Size())
	if err != nil {
		f.Close()
		return nil, err
	}

	r, err := newReader(src, ra, info.Size())
	if err != nil {
		if release != nil {
			release()
		}
		f.Close()
		return nil, err
	}

	r.closer = func() error {
		if release != nil {
			if err := release(); err != nil {
				f.Close()
				return err
			}
		}
		return f.Close()
	}
	return r, nil
}

// OpenBytes opens a database that is already fully in memory. The slice is
// used without copying and must not be modified afterwards.
func OpenBytes(b []byte) (*Reader, error) {
	return newReader(b, nil, int64(len(b)))
}

// NewReaderAt opens a database through an io.ReaderAt of the given size, which
// is useful for databases embedded in a bigger file or served by a block
// device. The caller keeps ownership of ra.
func NewReaderAt(ra io.ReaderAt, size int64) (*Reader, error) {
	if ra == nil {
		return nil, fmt.Errorf("awdb: nil io.ReaderAt")
	}
	if size < 0 {
		return nil, fmt.Errorf("awdb: negative size %d", size)
	}
	return newReader(nil, ra, size)
}

// newReader parses the header and computes the section offsets.
func newReader(src []byte, ra io.ReaderAt, size int64) (*Reader, error) {
	r := &Reader{src: src, ra: ra, size: size}

	if size < headerSizeLen {
		return nil, fmt.Errorf("%w: file is shorter than the header length field", ErrCorrupt)
	}

	lenBuf, err := r.region(0, headerSizeLen)
	if err != nil {
		return nil, err
	}
	metaLen := int64(binary.BigEndian.Uint16(lenBuf))
	if metaLen == 0 {
		return nil, fmt.Errorf("%w: empty JSON header", ErrCorrupt)
	}

	metaBuf, err := r.region(headerSizeLen, int(metaLen))
	if err != nil {
		return nil, err
	}
	meta, err := parseMetadata(metaBuf)
	if err != nil {
		return nil, err
	}

	r.meta = meta
	r.byteLen = meta.ByteLen
	r.nodeCount = uint64(meta.NodeCount)
	r.treeStart = headerSizeLen + metaLen

	treeSize := int64(r.nodeCount) * int64(2*meta.ByteLen)
	if r.treeStart > size-treeSize {
		return nil, fmt.Errorf("%w: search tree of %d nodes does not fit into %d bytes", ErrCorrupt, r.nodeCount, size)
	}
	r.dataStart = r.treeStart + treeSize

	return r, nil
}

// Metadata returns a copy of the parsed header.
func (r *Reader) Metadata() Metadata { return r.meta }

// Close releases the memory mapping and the underlying file. It is safe to
// call Close more than once. Calling Close while lookups are still running in
// other goroutines is not supported.
func (r *Reader) Close() error {
	if r.closed.Swap(true) {
		return nil
	}
	if r.closer != nil {
		return r.closer()
	}
	return nil
}

// region returns n bytes starting at off, either as a view on the in-memory
// file or as a freshly read buffer.
func (r *Reader) region(off int64, n int) ([]byte, error) {
	if off < 0 || n < 0 || off > r.size-int64(n) {
		return nil, fmt.Errorf("%w: read [%d,%d) outside a file of %d bytes", ErrCorrupt, off, off+int64(n), r.size)
	}
	if r.src != nil {
		return r.src[off : off+int64(n)], nil
	}
	buf := make([]byte, n)
	if _, err := r.ra.ReadAt(buf, off); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return buf, nil
}

// byteAt returns a single byte at off.
func (r *Reader) byteAt(off int64) (byte, error) {
	b, err := r.region(off, 1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// Lookup resolves a textual IP address, for example "8.8.8.8" or "2001:db8::1".
func (r *Reader) Lookup(ip string) (Record, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return Record{}, fmt.Errorf("%w: %q", ErrInvalidIP, ip)
	}
	return r.LookupAddr(addr)
}

// LookupBytes resolves a packed 4 or 16 byte address.
func (r *Reader) LookupBytes(ip []byte) (Record, error) {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return Record{}, fmt.Errorf("%w: %d byte address", ErrInvalidIP, len(ip))
	}
	return r.LookupAddr(addr)
}

// LookupAddr resolves a netip.Addr. IPv4-mapped addresses (::ffff:a.b.c.d) are
// accepted and treated as the IPv4 address they carry, matching what the
// reference readers do for "4_6" databases.
func (r *Reader) LookupAddr(addr netip.Addr) (Record, error) {
	if r.closed.Load() {
		return Record{}, ErrClosed
	}
	addr = addr.WithZone("")
	if !addr.IsValid() {
		return Record{}, ErrInvalidIP
	}

	off, err := r.recordOffset(addr)
	if err != nil {
		return Record{}, err
	}
	return r.decodeRecord(off)
}

// LookupJSON resolves ip and returns the record as a JSON object that keeps
// the column order of the database.
func (r *Reader) LookupJSON(ip string) ([]byte, error) {
	rec, err := r.Lookup(ip)
	if err != nil {
		return nil, err
	}
	return json.Marshal(rec)
}

// recordOffset walks the search tree and returns the absolute offset of the
// record the address resolves to.
func (r *Reader) recordOffset(addr netip.Addr) (int64, error) {
	packed, startBit, startNode, err := r.normalize(addr)
	if err != nil {
		return 0, err
	}

	node, err := r.findNode(packed, startBit, startNode)
	if err != nil {
		return 0, err
	}
	if node == r.nodeCount {
		return 0, ErrNotFound
	}
	if node < r.nodeCount {
		return 0, fmt.Errorf("%w: search tree ended at internal node %d", ErrCorrupt, node)
	}

	rel := int64(node) - int64(r.nodeCount) - dataSectionBias
	if rel < 0 {
		return 0, fmt.Errorf("%w: negative data offset for leaf %d", ErrCorrupt, node)
	}
	off := r.dataStart + rel
	if off >= r.size {
		return 0, fmt.Errorf("%w: record offset %d is beyond the end of the file", ErrCorrupt, off)
	}
	return off, nil
}

// findNode walks one bit per level and returns the value of the child it stops
// at: nodeCount means "empty", anything larger is a leaf holding a record
// offset.
//
// startBit and startNode are both 0 for a normal walk. A "4_6" database
// answers IPv4 queries from its ::ffff:0:0/96 branch, which the reference
// readers express by starting at bit 96 of the address and at node 96 of the
// tree.
func (r *Reader) findNode(packed []byte, startBit int, startNode uint64) (uint64, error) {
	bitCount := len(packed) * 8
	if startBit < 0 || startBit > bitCount {
		return 0, fmt.Errorf("%w: start bit %d outside 0..%d", ErrCorrupt, startBit, bitCount)
	}

	node := startNode
	for i := startBit; i < bitCount; i++ {
		if node >= r.nodeCount {
			break
		}
		bit := int(uint8(packed[i>>3])>>(7-uint(i&7))) & 1
		off := r.treeStart + int64(node)*int64(2*r.byteLen) + int64(bit*r.byteLen)
		raw, err := r.region(off, r.byteLen)
		if err != nil {
			return 0, err
		}
		node = beUint(raw)
	}
	return node, nil
}

// normalize maps an address to the packed form plus the bit offset and tree
// node the search starts at, rejecting addresses the database cannot answer
// for.
func (r *Reader) normalize(addr netip.Addr) ([]byte, int, uint64, error) {
	switch r.meta.IPVersion {
	case IPVersion4:
		v4 := addr
		if v4.Is4In6() {
			v4 = v4.Unmap()
		}
		if !v4.Is4() {
			return nil, 0, 0, fmt.Errorf("%w: %s", ErrIPv6QueryOnIPv4DB, addr)
		}
		b := v4.As4()
		return b[:], 0, 0, nil

	case IPVersion6:
		if addr.Is4() {
			return nil, 0, 0, fmt.Errorf("%w: %s", ErrIPv4QueryOnIPv6DB, addr)
		}
		b := addr.As16()
		return b[:], 0, 0, nil

	case IPVersion46:
		b := addr.As16() // IPv4 addresses become ::ffff:a.b.c.d
		if addr.Is4() || addr.Is4In6() {
			// The IPv4 part of a "4_6" tree is reached after the 96 bit
			// ::ffff:0:0/96 prefix, i.e. at bit 96 and node 96.
			return b[:], ipv4BranchBit, uint64(ipv4BranchBit), nil
		}
		return b[:], 0, 0, nil
	}

	return nil, 0, 0, fmt.Errorf("%w: ip_version %q", ErrUnsupported, r.meta.IPVersion)
}

// decodeRecord turns the raw record at off into a Record.
func (r *Reader) decodeRecord(off int64) (Record, error) {
	var raw []any

	switch r.meta.DecodeType {
	case DecodeStructure:
		value, _, err := r.decodeValue(off, 0)
		if err != nil {
			return Record{}, err
		}
		list, ok := value.([]any)
		if !ok {
			// A single scalar column is stored without an enclosing array.
			list = []any{value}
		}
		raw = list

	case DecodeDirect:
		list, err := r.decodeDirect(off)
		if err != nil {
			return Record{}, err
		}
		raw = list

	default:
		return Record{}, fmt.Errorf("%w: decode_type %d", ErrUnsupported, r.meta.DecodeType)
	}

	return r.meta.record(raw), nil
}

// beUint reads up to 8 bytes as a big endian unsigned integer.
func beUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}