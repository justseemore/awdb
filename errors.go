package awdb

import "errors"

// Errors returned by this package. All of them are wrapped with context, so
// compare them with errors.Is instead of ==.
var (
	// ErrNotFound means the search tree has no record for the address.
	ErrNotFound = errors.New("awdb: no record for the given address")

	// ErrCorrupt means the file is malformed: bad header, bad offsets, a
	// search tree that leads nowhere, an unknown data type or a pointer loop.
	ErrCorrupt = errors.New("awdb: corrupt database file")

	// ErrInvalidIP means the supplied value is not a valid IP address.
	ErrInvalidIP = errors.New("awdb: invalid IP address")

	// ErrIPv6QueryOnIPv4DB means an IPv6 address was looked up in a database
	// whose ip_version is "4".
	ErrIPv6QueryOnIPv4DB = errors.New("awdb: database is IPv4-only, an IPv6 address was supplied")

	// ErrIPv4QueryOnIPv6DB means an IPv4 address was looked up in a database
	// whose ip_version is "6".
	ErrIPv4QueryOnIPv6DB = errors.New("awdb: database is IPv6-only, an IPv4 address was supplied")

	// ErrUnsupported means the file uses a layout this package does not know.
	ErrUnsupported = errors.New("awdb: unsupported database layout")

	// ErrClosed means the reader has been closed.
	ErrClosed = errors.New("awdb: reader is closed")
)

// IsNotFound reports whether err reports a missing record.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }