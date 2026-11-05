package awdb

import (
	"bytes"
	"encoding/json"
)

// Record is one decoded database row.
//
// Columns keep the order they have in the database, which is what
// MarshalJSON uses, so a Record round trips to the same JSON a PHP or Python
// reader would print.
//
// The zero Record is a valid, empty record and marshals to {}.
type Record struct {
	keys   []string
	values []any
}

// record maps the raw decoded values onto the header columns.
//
// A database with a trailing list column stores one extra value, the list of
// rows; that case is rebuilt into a slice of nested Records.
func (m Metadata) record(raw []any) Record {
	if m.MultiArea != nil && len(raw) == len(m.Columns)+1 {
		values := make([]any, 0, len(m.Columns)+1)
		values = append(values, raw[:len(m.Columns)]...)

		rows := make([]Record, 0, 4)
		if list, ok := raw[len(m.Columns)].([]any); ok {
			for _, item := range list {
				row, ok := item.([]any)
				if !ok {
					continue
				}
				rowValues := make([]any, len(m.MultiArea.Keys))
				for i := range rowValues {
					if i < len(row) {
						rowValues[i] = row[i]
					}
				}
				rows = append(rows, Record{keys: m.MultiArea.Keys, values: rowValues})
			}
		}
		values = append(values, rows)

		keys := make([]string, 0, len(m.Columns)+1)
		keys = append(keys, m.Columns...)
		keys = append(keys, m.MultiArea.Name)
		return Record{keys: keys, values: values}
	}

	count := len(raw)
	if count > len(m.Columns) {
		count = len(m.Columns)
	}
	values := make([]any, count)
	copy(values, raw[:count])
	return Record{keys: m.Columns, values: values}
}

// Len returns the number of columns.
func (rec Record) Len() int { return len(rec.keys) }

// IsEmpty reports whether the record has no columns, which is the case when
// nothing was found or when the database row is empty.
func (rec Record) IsEmpty() bool { return len(rec.keys) == 0 }

// Keys returns the column names in database order.
func (rec Record) Keys() []string {
	out := make([]string, len(rec.keys))
	copy(out, rec.keys)
	return out
}

// Values returns the values in database order.
func (rec Record) Values() []any {
	out := make([]any, len(rec.values))
	copy(out, rec.values)
	return out
}

// Get returns the value stored under key.
func (rec Record) Get(key string) (any, bool) {
	for i, k := range rec.keys {
		if k == key {
			if i < len(rec.values) {
				return rec.values[i], true
			}
			return nil, false
		}
	}
	return nil, false
}

// Str returns the value of key as a string. Direct records (decode_type 2)
// always store strings, structure records only for string columns.
func (rec Record) Str(key string) string {
	if v, ok := rec.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Uint returns the value of key as an unsigned integer.
func (rec Record) Uint(key string) (uint64, bool) {
	v, ok := rec.Get(key)
	if !ok {
		return 0, false
	}
	u, ok := v.(uint64)
	return u, ok
}

// Int returns the value of key as a signed integer.
func (rec Record) Int(key string) (int64, bool) {
	v, ok := rec.Get(key)
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return n, true
	case uint64:
		return int64(n), true
	}
	return 0, false
}

// Float returns the value of key as a float64.
func (rec Record) Float(key string) (float64, bool) {
	v, ok := rec.Get(key)
	if !ok {
		return 0, false
	}
	switch f := v.(type) {
	case float64:
		return f, true
	case int64:
		return float64(f), true
	case uint64:
		return float64(f), true
	}
	return 0, false
}

// Rows returns the nested rows of a list column.
func (rec Record) Rows(key string) ([]Record, bool) {
	v, ok := rec.Get(key)
	if !ok {
		return nil, false
	}
	rows, ok := v.([]Record)
	return rows, ok
}

// Map converts the record into a plain map, which is handy when the column
// order does not matter. Nested rows become []map[string]any.
func (rec Record) Map() map[string]any {
	out := make(map[string]any, len(rec.keys))
	for i, key := range rec.keys {
		if i >= len(rec.values) {
			break
		}
		out[key] = plainValue(rec.values[i])
	}
	return out
}

// plainValue replaces nested Records by maps so the result contains only
// standard Go types.
func plainValue(v any) any {
	switch typed := v.(type) {
	case []Record:
		rows := make([]map[string]any, len(typed))
		for i := range typed {
			rows[i] = typed[i].Map()
		}
		return rows
	case []any:
		items := make([]any, len(typed))
		for i := range typed {
			items[i] = plainValue(typed[i])
		}
		return items
	}
	return v
}

// MarshalJSON writes the record as a JSON object in database column order.
func (rec Record) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	first := true
	for i, key := range rec.keys {
		if i >= len(rec.values) {
			break
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false

		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buf.Write(encodedKey)
		buf.WriteByte(':')

		encodedValue, err := json.Marshal(rec.values[i])
		if err != nil {
			return nil, err
		}
		buf.Write(encodedValue)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// String returns the JSON representation of the record.
func (rec Record) String() string {
	encoded, err := json.Marshal(rec)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}