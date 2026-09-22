//go:build darwin

package client

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/unix"
)

// processArgv returns the full argument vector of a running process (#2164).
//
// Read from the kernel rather than from anything the client persists. argv carries -passcode,
// -basic-auth and -token VALUES, so writing it to disk would put credentials at rest -- the
// class of leak closed in #2137, and the reason #2148 records flag NAMES only. Reading it here
// adds no exposure: it is already visible to this user through `ps`.
//
// KERN_PROCARGS2 rather than parsing `ps` output, because `ps` joins the arguments with spaces
// and nothing can reliably split them again: `-header "X-Api: v2"` comes back as three
// arguments instead of two. A silently mangled restart is worse than no restart at all.
//
// The buffer's layout is: a 32-bit argc, the executable path, NUL padding, then argc
// NUL-terminated argument strings (followed by the environment, which is deliberately not read
// -- it holds LFT_CLIENT_TOKEN among others, and nothing here needs it).
func processArgv(pid int) ([]string, error) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, fmt.Errorf("reading kern.procargs2 for pid %d: %w", pid, err)
	}
	if len(buf) < 4 {
		return nil, fmt.Errorf("kern.procargs2 for pid %d returned %d bytes", pid, len(buf))
	}

	// Native byte order. Both architectures macOS runs on are little-endian, and a wrong
	// reading here would surface immediately as an absurd argc rather than silently.
	argc := int(binary.LittleEndian.Uint32(buf[:4]))
	if argc <= 0 || argc > 1024 {
		return nil, fmt.Errorf("kern.procargs2 for pid %d reported argc=%d", pid, argc)
	}
	rest := buf[4:]

	// Step over the executable path, then over the NUL padding that follows it.
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		rest = rest[i:]
	} else {
		return nil, fmt.Errorf("kern.procargs2 for pid %d had no executable path", pid)
	}
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}

	argv := make([]string, 0, argc)
	for len(argv) < argc {
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			// The last argument can be unterminated if the buffer was truncated. Take what is
			// there rather than dropping it.
			if len(rest) > 0 {
				argv = append(argv, string(rest))
			}
			break
		}
		argv = append(argv, string(rest[:i]))
		rest = rest[i+1:]
	}

	if len(argv) == 0 || argv[0] == "" {
		return nil, fmt.Errorf("process %d reported an empty command line", pid)
	}
	return argv, nil
}
