//go:build !windows

package client

import (
	"errors"
	"syscall"
)

// isAddrInUse reports whether a listen failed because the address is taken.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
