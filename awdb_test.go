package awdb

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLookupIPv4Direct(t *testing.T) {
	db := newTestDB(3, IPVersion4, DecodeDirect, "country", "province", "city")
	db.insert(netip.MustParseAddr("1.0.0.0"), 8, 0, 0, directPayload("中国", "北京", "北京"))
	db.insert(netip.MustParseAddr("8.8.8.0"), 24, 0, 0, directPayload("美国", "加利福尼亚", "山景城"))
	db.insert(netip.MustParseAddr("114.114.114.114"), 32, 0, 0, directPayload("中国", "江苏省", "南京市"))

	reader := newTestReader(t, db)
	defer reader.Close()

	cases := []struct{ ip, country, province, city string }{
		{"1.2.3.4", "中国", "北京", "北京"},
		{"8.8.8.8", "美国", "加利福尼亚", "山景城"},
		{"114.114.114.114", "中国", "江苏省", "南京市"},
	}
	for _, tc := range cases {
		record := mustLookup(t, reader, tc.ip)
		if got := record.Str("country"); got != tc.country {
			t.Errorf("%s: country = %q, want %q", tc.ip, got, tc.country)
		}
		if got := record.Str("province"); got != tc.province {
			t.Errorf("%s: province = %q, want %q", tc.ip, got, tc.province)
		}
		if got := record.Str("city"); got != tc.city {
			t.Errorf("%s: city = %q, want %q", tc.ip, got, tc.city)
		}
		if record.Len() != 3 {
			t.Errorf("%s: Len() = %d, want 3", tc.ip, record.Len())
		}
	}

	if _, err := reader.Lookup("9.9.9.9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup(9.9.9.9) error = %v, want ErrNotFound", err)
	}
}

func TestLookupStructure(t *testing.T) {
	db := newTestDB(2, IPVersion4, DecodeStructure, "s", "ls", "u", "i", "f", "d", "arr")

	encoded := &datum{}
	encoded.array(7)
	encoded.str("hello")
	encoded.longStr(strings.Repeat("x", 300))
	encoded.uint(300, 2)
	encoded.int32(-42)
	encoded.float32(1.5)
	encoded.float64(3.25)
	encoded.array(3)
	encoded.uint(1, 1)
	encoded.uint(2, 1)
	encoded.uint(3, 1)

	db.insert(netip.MustParseAddr("10.0.0.0"), 8, 0, 0, encoded.buf)

	reader := newTestReader(t, db)
	defer reader.Close()

	record := mustLookup(t, reader, "10.1.2.3")

	if got := record.Str("s"); got != "hello" {
		t.Errorf("s = %q, want hello", got)
	}
	if got := record.Str("ls"); len(got) != 300 {
		t.Errorf("ls length = %d, want 300", len(got))
	}
	if got, ok := record.Uint("u"); !ok || got != 300 {
		t.Errorf("u = %d (%v), want 300", got, ok)
	}
	if got, ok := record.Int("i"); !ok || got != -42 {
		t.Errorf("i = %d (%v), want -42", got, ok)
	}
	if got, ok := record.Float("f"); !ok || got != 1.5 {
		t.Errorf("f = %v (%v), want 1.5", got, ok)
	}
	if got, ok := record.Float("d"); !ok || got != 3.25 {
		t.Errorf("d = %v (%v), want 3.25", got, ok)
	}

	raw, ok := record.Get("arr")
	items, itemsOK := raw.([]any)
	if !ok || !itemsOK || len(items) != 3 {
		t.Fatalf("arr = %v (%v), want 3 items", raw, itemsOK)
	}
	if got, ok := items[2].(uint64); !ok || got != 3 {
		t.Errorf("arr[2] = %v, want 3", items[2])
	}

	if got := record.String(); !strings.Contains(got, `"i":-42`) {
		t.Errorf("JSON = %s, want it to contain \"i\":-42", got)
	}
	if got := record.String(); !strings.Contains(got, `"f":1.5`) {
		t.Errorf("JSON = %s, want it to contain \"f\":1.5", got)
	}
}

func TestLookupMultiArea(t *testing.T) {
	db := newTestDB(2, IPVersion4, DecodeStructure, "country", "multi_area", []string{"code", "name"})

	encoded := &datum{}
	encoded.array(2)
	encoded.str("中国")
	encoded.array(2)
	encoded.array(2)
	encoded.str("100000")
	encoded.str("北京")
	encoded.array(2)
	encoded.str("200000")
	encoded.str("上海")

	db.insert(netip.MustParseAddr("1.0.0.0"), 8, 0, 0, encoded.buf)

	reader := newTestReader(t, db)
	defer reader.Close()

	record := mustLookup(t, reader, "1.2.3.4")
	if got := record.Str("country"); got != "中国" {
		t.Errorf("country = %q", got)
	}

	rows, ok := record.Rows("multi_area")
	if !ok || len(rows) != 2 {
		t.Fatalf("multi_area = %v (%v), want 2 rows", rows, ok)
	}
	if got := rows[1].Str("name"); got != "上海" {
		t.Errorf("rows[1].name = %q, want 上海", got)
	}

	want := `{"country":"中国","multi_area":[{"code":"100000","name":"北京"},{"code":"200000","name":"上海"}]}`
	if got := record.String(); got != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}

	mapped := record.Map()
	list, ok := mapped["multi_area"].([]map[string]any)
	if !ok || len(list) != 2 || list[0]["code"] != "100000" {
		t.Fatalf("Map()[multi_area] = %v", mapped["multi_area"])
	}
}

func TestLookupIPv6(t *testing.T) {
	db := newTestDB(4, IPVersion6, DecodeStructure, "country", "asn")

	encoded := &datum{}
	encoded.array(2)
	encoded.str("美国")
	encoded.uint(15169, 4)

	db.insert(netip.MustParseAddr("2001:4860::"), 32, 0, 0, encoded.buf)

	reader := newTestReader(t, db)
	defer reader.Close()

	record := mustLookup(t, reader, "2001:4860:4860::8888")
	if got := record.Str("country"); got != "美国" {
		t.Errorf("country = %q", got)
	}
	if got, ok := record.Uint("asn"); !ok || got != 15169 {
		t.Errorf("asn = %d (%v)", got, ok)
	}

	if _, err := reader.Lookup("2001:4861::1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Lookup(2001:4861::1) error = %v, want ErrNotFound", err)
	}
	if _, err := reader.Lookup("8.8.8.8"); !errors.Is(err, ErrIPv4QueryOnIPv6DB) {
		t.Errorf("Lookup(8.8.8.8) error = %v, want ErrIPv4QueryOnIPv6DB", err)
	}
}

func TestLookupIPv4In46Database(t *testing.T) {
	db := newTestDB(3, IPVersion46, DecodeDirect, "country", "city")

	// A "4_6" tree reaches its IPv4 part after the 96 bit ::ffff:0:0/96 prefix.
	root := db.chainLeft(96)
	db.insert(netip.MustParseAddr("1.2.3.0"), 24, 96, root, directPayload("中国", "北京"))

	reader := newTestReader(t, db)
	defer reader.Close()

	for _, ip := range []string{"1.2.3.9", "::ffff:1.2.3.9", "0:0:0:0:0:ffff:1.2.3.9"} {
		record := mustLookup(t, reader, ip)
		if got := record.Str("city"); got != "北京" {
			t.Errorf("%s: city = %q, want 北京", ip, got)
		}
	}

	if _, err := reader.Lookup("1.2.4.9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Lookup(1.2.4.9) error = %v, want ErrNotFound", err)
	}
}

func TestLookupErrors(t *testing.T) {
	db := newTestDB(2, IPVersion4, DecodeDirect, "country")
	db.insert(netip.MustParseAddr("1.0.0.0"), 8, 0, 0, directPayload("中国"))

	reader := newTestReader(t, db)
	defer reader.Close()

	if _, err := reader.Lookup("not-an-ip"); !errors.Is(err, ErrInvalidIP) {
		t.Errorf("invalid address error = %v, want ErrInvalidIP", err)
	}
	if _, err := reader.Lookup("2001:db8::1"); !errors.Is(err, ErrIPv6QueryOnIPv4DB) {
		t.Errorf("IPv6 in IPv4 database error = %v, want ErrIPv6QueryOnIPv4DB", err)
	}
	if _, err := reader.LookupBytes([]byte{1, 2, 3}); !errors.Is(err, ErrInvalidIP) {
		t.Errorf("short address error = %v, want ErrInvalidIP", err)
	}
	if _, err := reader.Lookup("9.9.9.9"); !IsNotFound(err) {
		t.Errorf("missing address error = %v, want ErrNotFound", err)
	}

	// IPv4 addresses are accepted through netip and through raw bytes.
	if record, err := reader.LookupBytes([]byte{1, 2, 3, 4}); err != nil || record.Str("country") != "中国" {
		t.Errorf("LookupBytes(1.2.3.4) = %v, %v", record, err)
	}
	if record, err := reader.LookupAddr(netip.MustParseAddr("1.2.3.4")); err != nil || record.Str("country") != "中国" {
		t.Errorf("LookupAddr(1.2.3.4) = %v, %v", record, err)
	}
}

func TestOpenFileAndClose(t *testing.T) {
	db := newTestDB(3, IPVersion4, DecodeDirect, "country", "city")
	db.insert(netip.MustParseAddr("1.0.0.0"), 8, 0, 0, directPayload("中国", "北京"))

	path := filepath.Join(t.TempDir(), "test.awdb")
	if err := os.WriteFile(path, db.mustBytes(t), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reader, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := mustLookup(t, reader, "1.2.3.4").Str("city"); got != "北京" {
		t.Errorf("city = %q", got)
	}
	if reader.Metadata().NodeCount == 0 {
		t.Error("NodeCount = 0")
	}

	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := reader.Lookup("1.2.3.4"); !errors.Is(err, ErrClosed) {
		t.Errorf("lookup after Close error = %v, want ErrClosed", err)
	}
	if err := reader.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestOpenCorrupt(t *testing.T) {
	if _, err := OpenBytes(nil); !errors.Is(err, ErrCorrupt) {
		t.Errorf("OpenBytes(nil) error = %v, want ErrCorrupt", err)
	}
	if _, err := OpenBytes([]byte{0, 9, '{'}); err == nil {
		t.Error("OpenBytes(short header) error = nil, want an error")
	}

	db := newTestDB(3, IPVersion4, DecodeDirect, "country")
	db.insert(netip.MustParseAddr("1.0.0.0"), 8, 0, 0, directPayload("CN"))
	raw := db.mustBytes(t)

	if _, err := OpenBytes(raw[:len(raw)/2]); err == nil {
		t.Error("truncated database accepted")
	}
	if _, err := OpenBytes(raw[:8]); err == nil {
		t.Error("truncated header accepted")
	}

	if _, err := OpenBytes([]byte(`{"node_count":1,"ip_version":"5","decode_type":1,"byte_len":3,"columns":["a"]}`)); err == nil {
		t.Error("unknown ip_version accepted")
	}
}

func TestPointerCycle(t *testing.T) {
	db := newTestDB(2, IPVersion4, DecodeStructure, "v")

	// The record points at itself, which must be reported instead of looping.
	payload := (&datum{}).pointer(int64(len(db.data)), 2)
	db.insert(netip.MustParseAddr("1.0.0.0"), 8, 0, 0, payload.buf)

	reader := newTestReader(t, db)
	defer reader.Close()

	if _, err := reader.Lookup("1.1.1.1"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("pointer loop error = %v, want ErrCorrupt", err)
	}
}

func TestParseMetadata(t *testing.T) {
	meta, err := parseMetadata([]byte(`{"node_count":7,"ip_version":4,"decode_type":2,"byte_len":3,"columns":["country","city"]}`))
	if err != nil {
		t.Fatalf("parseMetadata: %v", err)
	}
	if meta.IPVersion != IPVersion4 || meta.NodeCount != 7 || meta.ByteLen != 3 || meta.DecodeType != DecodeDirect {
		t.Errorf("unexpected metadata: %+v", meta)
	}
	if len(meta.Columns) != 2 || meta.Columns[1] != "city" || meta.MultiArea != nil {
		t.Errorf("unexpected columns: %+v", meta)
	}

	meta, err = parseMetadata([]byte(`{"node_count":7,"ip_version":"4_6","decode_type":1,"byte_len":4,"columns":["country","multi_area",["code","name"]]}`))
	if err != nil {
		t.Fatalf("parseMetadata: %v", err)
	}
	if meta.IPVersion != IPVersion46 || meta.MultiArea == nil {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
	if meta.MultiArea.Name != "multi_area" || len(meta.MultiArea.Keys) != 2 {
		t.Errorf("unexpected multi area: %+v", meta.MultiArea)
	}
	if len(meta.Columns) != 1 || meta.Columns[0] != "country" {
		t.Errorf("unexpected columns: %+v", meta.Columns)
	}

	if _, err := parseMetadata([]byte(`{"node_count":1,"ip_version":"4","decode_type":9,"byte_len":3,"columns":["a"]}`)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("decode_type 9 error = %v, want ErrUnsupported", err)
	}
}

func TestRecordBasics(t *testing.T) {
	empty := Record{}
	if !empty.IsEmpty() || empty.String() != "{}" {
		t.Errorf("empty record = %q", empty.String())
	}
	if _, ok := empty.Get("x"); ok {
		t.Error("Get on empty record returned a value")
	}

	db := newTestDB(2, IPVersion4, DecodeDirect, "country", "city")
	db.insert(netip.MustParseAddr("1.0.0.0"), 8, 0, 0, directPayload("中国", "北京"))
	reader := newTestReader(t, db)
	defer reader.Close()

	record := mustLookup(t, reader, "1.2.3.4")
	keys := record.Keys()
	if len(keys) != 2 || keys[0] != "country" || keys[1] != "city" {
		t.Fatalf("Keys() = %v", keys)
	}
	if values := record.Values(); len(values) != 2 || values[0] != "中国" {
		t.Fatalf("Values() = %v", values)
	}
	if mapped := record.Map(); mapped["city"] != "北京" {
		t.Fatalf("Map() = %v", mapped)
	}

	encoded, err := reader.LookupJSON("1.2.3.4")
	if err != nil {
		t.Fatalf("LookupJSON: %v", err)
	}
	if string(encoded) != `{"country":"中国","city":"北京"}` {
		t.Errorf("LookupJSON = %s", encoded)
	}
	if _, err := reader.LookupJSON("9.9.9.9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("LookupJSON on missing address error = %v", err)
	}
}

func TestConcurrentLookup(t *testing.T) {
	db := newTestDB(3, IPVersion4, DecodeDirect, "country", "city")
	db.insert(netip.MustParseAddr("1.0.0.0"), 8, 0, 0, directPayload("中国", "北京"))
	db.insert(netip.MustParseAddr("8.8.8.0"), 24, 0, 0, directPayload("美国", "山景城"))

	reader := newTestReader(t, db)
	defer reader.Close()

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if _, err := reader.Lookup("8.8.8.8"); err != nil {
					t.Errorf("Lookup(8.8.8.8): %v", err)
					return
				}
				if _, err := reader.Lookup("1.2.3.4"); err != nil {
					t.Errorf("Lookup(1.2.3.4): %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestOpenWithPreload(t *testing.T) {
	db := newTestDB(3, IPVersion4, DecodeDirect, "country", "city")
	db.insert(netip.MustParseAddr("1.0.0.0"), 8, 0, 0, directPayload("中国", "北京"))

	path := filepath.Join(t.TempDir(), "preload.awdb")
	if err := os.WriteFile(path, db.mustBytes(t), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reader, err := Open(path, WithPreload(true))
	if err != nil {
		t.Fatalf("Open(preload): %v", err)
	}
	defer reader.Close()

	if got := mustLookup(t, reader, "1.2.3.4").Str("city"); got != "北京" {
		t.Errorf("city = %q, want 北京", got)
	}
	if reader.Metadata().NodeCount == 0 {
		t.Error("NodeCount = 0")
	}

	// 预加载不保留句柄，Close 仍然幂等
	if err := reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := reader.Lookup("1.2.3.4"); !errors.Is(err, ErrClosed) {
		t.Errorf("lookup after Close error = %v, want ErrClosed", err)
	}

	if _, err := Open(filepath.Join(t.TempDir(), "missing.awdb"), WithPreload(true)); err == nil {
		t.Error("missing file accepted")
	}
}

func BenchmarkLookupIPv4(b *testing.B) {
	db := newTestDB(3, IPVersion4, DecodeDirect, "country", "province", "city")
	db.insert(netip.MustParseAddr("1.0.0.0"), 8, 0, 0, directPayload("中国", "北京", "北京"))

	reader := newTestReader(b, db)
	defer reader.Close()

	addr := netip.MustParseAddr("1.2.3.4")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := reader.LookupAddr(addr); err != nil {
			b.Fatalf("LookupAddr: %v", err)
		}
	}
}