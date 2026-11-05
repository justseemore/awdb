package awdb

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"net/netip"
	"strings"
	"testing"
)

// testDB writes a synthetic AWDB file, so the reader can be tested without the
// commercial database files.
type testDB struct {
	byteLen    int
	ipVersion  string
	decodeType int
	columns    []any
	tree       [][2]int // -1 means "not set yet"
	leaves     []testLeaf
	data       []byte
}

type testLeaf struct {
	node int
	bit  int
	rel  int64
}

func newTestDB(byteLen int, ipVersion string, decodeType int, columns ...any) *testDB {
	return &testDB{
		byteLen:    byteLen,
		ipVersion:  ipVersion,
		decodeType: decodeType,
		columns:    columns,
		tree:       [][2]int{{-1, -1}},
	}
}

func (db *testDB) newNode() int {
	db.tree = append(db.tree, [2]int{-1, -1})
	return len(db.tree) - 1
}

// chainLeft turns nodes 0..n-1 into a left spine ending at node n and returns
// n. A "4_6" database reaches its IPv4 part after 96 leading zero bits, so the
// IPv4 tree of such a file conventionally starts at node 96.
func (db *testDB) chainLeft(n int) int {
	for len(db.tree) <= n {
		db.newNode()
	}
	for i := 0; i < n; i++ {
		db.tree[i][0] = i + 1
	}
	return n
}

func (db *testDB) addData(payload []byte) int64 {
	rel := int64(len(db.data))
	db.data = append(db.data, payload...)
	return rel
}

// insert stores payload for ip/prefixLen, starting the walk at bit startBit of
// node root, the same way a database builder would write the file.
func (db *testDB) insert(ip netip.Addr, prefixLen, startBit, root int, payload []byte) {
	rel := db.addData(payload)
	packed := db.packed(ip)

	node := root
	for i := startBit; i < startBit+prefixLen; i++ {
		bit := int(uint8(packed[i>>3])>>(7-uint(i&7))) & 1
		if i == startBit+prefixLen-1 {
			db.leaves = append(db.leaves, testLeaf{node: node, bit: bit, rel: rel})
			return
		}
		next := db.tree[node][bit]
		if next < 0 {
			next = db.newNode()
			db.tree[node][bit] = next
		}
		node = next
	}
}

// packed returns the address in the width the database stores: 4 bytes for an
// IPv4 database, otherwise the 16 byte form, where an IPv4 address becomes
// ::ffff:a.b.c.d.
func (db *testDB) packed(ip netip.Addr) []byte {
	if db.ipVersion == IPVersion4 && ip.Is4() {
		v4 := ip.As4()
		return v4[:]
	}
	v6 := ip.As16()
	return v6[:]
}

// mustBytes serialises the database: header, search tree, data section.
func (db *testDB) mustBytes(tb testing.TB) []byte {
	tb.Helper()

	nodeCount := len(db.tree)
	values := make([][2]uint64, nodeCount)
	for i := range db.tree {
		for bit := 0; bit < 2; bit++ {
			child := db.tree[i][bit]
			if child < 0 {
				values[i][bit] = uint64(nodeCount) // empty branch
			} else {
				values[i][bit] = uint64(child)
			}
		}
	}
	for _, leaf := range db.leaves {
		values[leaf.node][leaf.bit] = uint64(nodeCount) + dataSectionBias + uint64(leaf.rel)
	}

	header, err := json.Marshal(map[string]any{
		"node_count":  nodeCount,
		"ip_version":  db.ipVersion,
		"decode_type": db.decodeType,
		"byte_len":    db.byteLen,
		"columns":     db.columns,
	})
	if err != nil {
		tb.Fatalf("marshal header: %v", err)
	}
	if len(header) > 0xFFFF {
		tb.Fatalf("header too large: %d bytes", len(header))
	}

	out := make([]byte, 0, 2+len(header)+nodeCount*2*db.byteLen+len(db.data))
	var sizeBuf [2]byte
	binary.BigEndian.PutUint16(sizeBuf[:], uint16(len(header)))
	out = append(out, sizeBuf[:]...)
	out = append(out, header...)

	var nodeBuf [8]byte
	for i := 0; i < nodeCount; i++ {
		for bit := 0; bit < 2; bit++ {
			value := values[i][bit]
			for k := 0; k < db.byteLen; k++ {
				nodeBuf[k] = byte(value >> (8 * uint(db.byteLen-1-k)))
			}
			out = append(out, nodeBuf[:db.byteLen]...)
		}
	}

	return append(out, db.data...)
}

// datum encodes values with the type tags used by decode_type 1.
type datum struct {
	buf []byte
}

func (d *datum) tag(tag uint8, extra ...byte) *datum {
	d.buf = append(d.buf, tag)
	d.buf = append(d.buf, extra...)
	return d
}

func (d *datum) array(count int) *datum {
	return d.tag(dataTypeArray, byte(count))
}

func (d *datum) str(s string) *datum {
	d.tag(dataTypeString, byte(len(s)))
	d.buf = append(d.buf, s...)
	return d
}

func (d *datum) longStr(s string) *datum {
	d.tag(dataTypeLongString, 2)
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(s)))
	d.buf = append(d.buf, lenBuf[:]...)
	d.buf = append(d.buf, s...)
	return d
}

func (d *datum) uint(value uint64, size int) *datum {
	d.tag(dataTypeUint, byte(size))
	d.buf = append(d.buf, beBytes(value, size)...)
	return d
}

func (d *datum) int32(value int32) *datum {
	d.tag(dataTypeInt)
	d.buf = append(d.buf, beBytes(uint64(uint32(value)), 4)...)
	return d
}

func (d *datum) float32(value float32) *datum {
	d.tag(dataTypeFloat)
	d.buf = append(d.buf, beBytes(uint64(math.Float32bits(value)), 4)...)
	return d
}

func (d *datum) float64(value float64) *datum {
	d.tag(dataTypeDouble)
	d.buf = append(d.buf, beBytes(math.Float64bits(value), 8)...)
	return d
}

func (d *datum) pointer(rel int64, size int) *datum {
	d.tag(dataTypePointer, byte(size))
	d.buf = append(d.buf, beBytes(uint64(rel), size)...)
	return d
}

// beBytes encodes value as size big endian bytes.
func beBytes(value uint64, size int) []byte {
	out := make([]byte, size)
	for i := 0; i < size; i++ {
		out[i] = byte(value >> (8 * uint(size-1-i)))
	}
	return out
}

// directPayload encodes a decode_type 2 record: uint32 length plus a tab
// separated line.
func directPayload(fields ...string) []byte {
	body := strings.Join(fields, "\t")
	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(out, uint32(len(body)))
	copy(out[4:], body)
	return out
}

func newTestReader(tb testing.TB, db *testDB) *Reader {
	tb.Helper()
	reader, err := OpenBytes(db.mustBytes(tb))
	if err != nil {
		tb.Fatalf("OpenBytes: %v", err)
	}
	return reader
}

func mustLookup(tb testing.TB, reader *Reader, ip string) Record {
	tb.Helper()
	record, err := reader.Lookup(ip)
	if err != nil {
		tb.Fatalf("Lookup(%q): %v", ip, err)
	}
	return record
}