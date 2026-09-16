package geo

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenResolverWithoutADatabaseIsUnavailable is the graceful-absence contract. No
// MaxMind file ships with the server, so this is the default state of every deployment
// and it must be reported as "unavailable", never as a startup failure.
func TestOpenResolverWithoutADatabaseIsUnavailable(t *testing.T) {
	t.Run("empty path", func(t *testing.T) {
		res, err := OpenResolver("")
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("got err %v, want ErrUnavailable", err)
		}
		if res != nil {
			t.Errorf("got a resolver %v, want nil", res)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "GeoLite2-Country.mmdb")
		res, err := OpenResolver(path)
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("got err %v, want ErrUnavailable", err)
		}
		if res != nil {
			t.Errorf("got a resolver %v, want nil", res)
		}
	})
}

// TestOpenResolverSeparatesAnUnsetPathFromAMistypedOne is the assertion that would have
// caught #1938. Both states are non-fatal and both leave the feature off, so every other
// test in this file passes either way; what was missing is that they were the SAME error,
// so nothing downstream could tell an operator's typo from the default.
//
// Asserted on the error identity and on the path being named, never on "an error
// occurred": every failure mode in OpenResolver returns an error, so that alone is
// satisfied by any of the wrong causes.
func TestOpenResolverSeparatesAnUnsetPathFromAMistypedOne(t *testing.T) {
	if _, unset := OpenResolver(""); errors.Is(unset, ErrNotFound) {
		t.Errorf("an unset path reported ErrNotFound (%v) -- nothing was configured to be missing", unset)
	}

	typo := filepath.Join(t.TempDir(), "GeoLite2-Cuntry.mmdb")
	_, err := OpenResolver(typo)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("a configured path with no file at it: got %v, want ErrNotFound", err)
	}
	// The panel names the path back to the admin so a typo is self-evident, and it reads
	// it from here. A sentinel that dropped the path would leave the message no better
	// than the one it replaced.
	if err == nil || !strings.Contains(err.Error(), typo) {
		t.Errorf("error %v does not name the configured path %q", err, typo)
	}
	// Still inside the graceful-absence contract: the server must go on treating this as
	// "feature off", never as a startup failure.
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("a missing configured file stopped matching ErrUnavailable: %v", err)
	}
}

// TestOpenResolverWithACorruptDatabaseFails distinguishes a file that is present but
// unusable from one that is simply absent: the first is an operator error worth
// surfacing, the second is normal.
func TestOpenResolverWithACorruptDatabaseFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.mmdb")
	if err := os.WriteFile(path, []byte("this is not a MaxMind database"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	res, err := OpenResolver(path)
	if err == nil {
		t.Fatalf("got no error for a corrupt database")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Errorf("a corrupt database reported itself as merely unavailable: %v", err)
	}
	if res != nil {
		t.Errorf("got a resolver %v, want nil", res)
	}
}

// TestNilResolverIsSafe covers the paths that run before or after a database is open.
func TestNilResolverIsSafe(t *testing.T) {
	var r *mmdbResolver
	if country, ok := r.Country(netip.MustParseAddr("1.1.1.1")); ok || country != "" {
		t.Errorf("got (%q, %v), want (\"\", false)", country, ok)
	}
	if err := r.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestInvalidAddressResolvesToNothing guards the zero netip.Addr, which is what an
// unparsed or absent client address arrives as.
func TestInvalidAddressResolvesToNothing(t *testing.T) {
	r := &mmdbResolver{}
	if country, ok := r.Country(netip.Addr{}); ok || country != "" {
		t.Errorf("got (%q, %v), want (\"\", false)", country, ok)
	}
}
