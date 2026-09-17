package geo

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// A real .mmdb file, assembled in memory, so the decode path can be exercised without a
// vendor database (#1993).
//
// Nothing in CI had ever run Country() against an mmdb at all: every test here either
// stopped at OpenResolver or compared countryPaths against a second copy of itself, and the
// only tests that decode a record skip unless LFT_GEO_TEST_DB names a file no one may
// redistribute. So the assertion "this build decodes the schema its vendors use" had no
// evidence behind it in the one place that runs on every commit.
//
// Written by hand rather than pulled in from mmdbwriter: a test-only dependency on a second
// MaxMind library to prove a four-line decode works is a worse trade than 80 lines of
// encoder. The file it produces is read back by the SAME reader production uses
// (maxminddb.Open, via OpenResolver), so a mistake here surfaces as a broken fixture rather
// than as a passing test over a fake.
//
// Layout, per the MaxMind DB 2.0 specification:
//
//	[ search tree: 1 node, 24-bit records ][ 16 zero bytes ][ data section ][ marker ][ metadata ]
//
// The tree is the smallest one that can still answer both ways. The single node sends bit 0
// of the address left to the record and right to "empty", so 0.0.0.0-127.255.255.255
// resolves and 128.0.0.0-255.255.255.255 does not -- which is what lets the same fixture
// prove that a private address stays out of the distribution.
const (
	fixtureNodeCount  = 1
	fixtureRecordSize = 24
	// dataSectionSeparator is 16 zero bytes between the tree and the data section, and the
	// reason a tree record pointing at data section offset 0 has the value nodeCount+16.
	dataSectionSeparator = 16
)

// writeTestMMDB writes a one-record database carrying record and returns its path.
func writeTestMMDB(t *testing.T, record map[string]any) string {
	t.Helper()

	data := encodeMMDBValue(t, record)

	// Tree: left record (bit 0) points at data section offset 0, right record is "empty".
	// A data pointer is the offset plus nodeCount plus the separator, so offset 0 is 17.
	tree := make([]byte, 0, fixtureNodeCount*fixtureRecordSize*2/8)
	tree = appendUint24(tree, fixtureNodeCount+dataSectionSeparator)
	tree = appendUint24(tree, fixtureNodeCount)

	file := make([]byte, 0, 512)
	file = append(file, tree...)
	file = append(file, make([]byte, dataSectionSeparator)...)
	file = append(file, data...)
	file = append(file, []byte("\xAB\xCD\xEFMaxMind.com")...)
	file = append(file, encodeMMDBValue(t, map[string]any{
		"node_count":                  uint32(fixtureNodeCount),
		"record_size":                 uint32(fixtureRecordSize),
		"ip_version":                  uint32(4),
		"database_type":               "GeoLite2-Country",
		"languages":                   []string{"en"},
		"binary_format_major_version": uint32(2),
		"binary_format_minor_version": uint32(0),
		"build_epoch":                 uint32(1_757_000_000),
		"description":                 map[string]any{"en": "lfr-tunnel test fixture"},
	})...)

	path := filepath.Join(t.TempDir(), "fixture.mmdb")
	if err := os.WriteFile(path, file, 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

func appendUint24(b []byte, v uint32) []byte {
	return append(b, byte(v>>16), byte(v>>8), byte(v))
}

// MMDB data types used by this fixture. The full set is in the specification; these are the
// four a country record and a metadata block need.
const (
	typeString = 2
	typeUint32 = 6
	typeMap    = 7
	typeArray  = 11
)

// encodeMMDBValue encodes v in the MaxMind DB data-section format.
//
// Sizes above 28 need a multi-byte length prefix, which nothing in this fixture reaches --
// so the encoder refuses them rather than emitting a length it never learned to write. A
// silently wrong length would surface as an unreadable file, which is a confusing way to be
// told the fixture outgrew its encoder.
func encodeMMDBValue(t *testing.T, v any) []byte {
	t.Helper()

	switch val := v.(type) {
	case string:
		return append(controlBytes(t, typeString, len(val)), val...)
	case uint32:
		var full [4]byte
		binary.BigEndian.PutUint32(full[:], val)
		trimmed := full[:]
		for len(trimmed) > 0 && trimmed[0] == 0 {
			trimmed = trimmed[1:]
		}
		return append(controlBytes(t, typeUint32, len(trimmed)), trimmed...)
	case []string:
		out := controlBytes(t, typeArray, len(val))
		for _, item := range val {
			out = append(out, encodeMMDBValue(t, item)...)
		}
		return out
	case map[string]any:
		out := controlBytes(t, typeMap, len(val))
		for key, item := range val {
			out = append(out, encodeMMDBValue(t, key)...)
			out = append(out, encodeMMDBValue(t, item)...)
		}
		return out
	default:
		t.Fatalf("fixture encoder has no case for %T", v)
		return nil
	}
}

func controlBytes(t *testing.T, mmdbType, size int) []byte {
	t.Helper()

	if size > 28 {
		t.Fatalf("fixture encoder only writes sizes under 29, got %d", size)
	}
	if mmdbType < 8 {
		return []byte{byte(mmdbType<<5 | size)}
	}
	// Extended type: the top three bits are zero and the following byte carries type-7.
	return []byte{byte(size), byte(mmdbType - 7)}
}
