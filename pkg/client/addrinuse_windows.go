//go:build windows

package client

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isAddrInUse reports whether a listen failed because the address is taken. On Windows that is
// WSAEADDRINUSE, whose message ("Only one usage of each socket address ... is normally
// permitted") never matched the "address already in use" text the inspector used to look for, so
// it never moved to the next port there.
func isAddrInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE)
}
