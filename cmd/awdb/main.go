// Command awdb is a small command line front end for AWDB databases.
//
// Usage:
//
//	awdb -db ip.awdb 8.8.8.8 114.114.114.114
//	awdb -db ip.awdb -pretty 8.8.8.8
//	cat ips.txt | awdb -db ip.awdb
//	awdb -db ip.awdb -meta
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/justseemore/awdb"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "awdb:", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("awdb", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dbPath := flags.String("db", "", "path of the .awdb database file")
	metaOnly := flags.Bool("meta", false, "print the database header as JSON and exit")
	pretty := flags.Bool("pretty", false, "indent the JSON output")
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), "usage: awdb -db <file.awdb> [ip ...]\n\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *dbPath == "" {
		flags.Usage()
		return errors.New("missing -db")
	}

	reader, err := awdb.Open(*dbPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	if *metaOnly {
		meta := reader.Metadata()
		return writeJSON(metaOutput{
			NodeCount:  meta.NodeCount,
			IPVersion:  meta.IPVersion,
			DecodeType: meta.DecodeType,
			ByteLen:    meta.ByteLen,
			Columns:    meta.Columns,
			MultiArea:  meta.MultiArea,
		}, true)
	}

	ips := flags.Args()
	if len(ips) == 0 {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			ips = append(ips, line)
		}
		if err := scanner.Err(); err != nil {
			return err
		}
	}
	if len(ips) == 0 {
		flags.Usage()
		return errors.New("no IP address to look up")
	}

	for _, ip := range ips {
		record, err := reader.Lookup(ip)
		if err != nil {
			if !awdb.IsNotFound(err) {
				fmt.Fprintf(os.Stderr, "%s: %v\n", ip, err)
				continue
			}
			record = awdb.Record{}
		}
		if err := writeJSON(record, *pretty); err != nil {
			return err
		}
	}
	return nil
}

// metaOutput is the shape printed by -meta.
type metaOutput struct {
	NodeCount  uint32          `json:"node_count"`
	IPVersion  string          `json:"ip_version"`
	DecodeType int             `json:"decode_type"`
	ByteLen    int             `json:"byte_len"`
	Columns    []string        `json:"columns"`
	MultiArea  *awdb.MultiArea `json:"multi_area,omitempty"`
}

func writeJSON(value any, pretty bool) error {
	encoder := json.NewEncoder(os.Stdout)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(value)
}