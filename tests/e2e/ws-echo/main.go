// Command ws-echo is the E2E stack's WebSocket target (#2249).
//
// The stack had no origin that could be upgraded. mock-target is nginx serving a static file, so
// every existing spec drives ordinary request/response traffic and the one shape that had broken
// in production -- a visitor's WebSocket, 502 since 2026-06-10 (#2179) and then hanging 5s
// through the client interceptor (#2213) -- could not be driven at all. Both halves were fixed
// with unit tests around trackingTransport and interceptorTransport; nothing put a socket in at
// one end of the real data plane and read it out of the other.
//
// It answers two things:
//
//	GET /            200 text/plain "ws-echo"   -- ordinary traffic, so a spec can tell
//	                                               "the tunnel is down" from "the upgrade
//	                                               was refused". #2179 was exactly the
//	                                               second with the first still working.
//	GET /echo        101 + echo every frame     -- the subject.
//
// Written against the stdlib rather than gorilla/websocket so the module needs no dependencies;
// see go.mod. The frame codec below is deliberately the smallest thing that is correct for what
// the specs send: single unfragmented frames under 64KiB. Anything outside that is refused
// loudly rather than mishandled quietly, because a fixture that half-works reports a defect in
// the subject (github-workflow SKILL 5c, rule 5).
package main

import (
	"bufio"
	// SHA-1 is what RFC 6455 section 4.2.2 specifies for the handshake accept value. It is a
	// fixed part of the protocol, not a security choice this fixture is making.
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// The GUID RFC 6455 section 1.3 concatenates with the client's key.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opContinuation byte = 0x0
	opText         byte = 0x1
	opBinary       byte = 0x2
	opClose        byte = 0x8
	opPing         byte = 0x9
	opPong         byte = 0xA
)

// maxPayload bounds a single frame. The specs send a short marker string; anything larger is a
// mistake in a test, and refusing it keeps this fixture from quietly allocating on an input it
// was never meant to receive.
const maxPayload = 64 << 10

// idleTimeout closes a session nothing is using. Without it a dropped tunnel leaves a goroutine
// blocked in ReadFull for the lifetime of the stack, and the stack outlives the suite when
// E2E_KEEP_STACK is set.
const idleTimeout = 2 * time.Minute

type frame struct {
	fin     bool
	opcode  byte
	payload []byte
}

func acceptKey(key string) string {
	h := sha1.New()
	// Hash.Write is documented never to return an error.
	if _, err := io.WriteString(h, key+wsGUID); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func readFrame(r *bufio.Reader) (frame, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return frame{}, err
	}
	f := frame{fin: hdr[0]&0x80 != 0, opcode: hdr[0] & 0x0F}
	masked := hdr[1]&0x80 != 0
	length := uint64(hdr[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return frame{}, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return frame{}, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if length > maxPayload {
		return frame{}, fmt.Errorf("frame of %d bytes exceeds this fixture's %d-byte limit", length, maxPayload)
	}
	var key [4]byte
	if masked {
		if _, err := io.ReadFull(r, key[:]); err != nil {
			return frame{}, err
		}
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return frame{}, err
	}
	if masked {
		for i := range buf {
			buf[i] ^= key[i%4]
		}
	}
	f.payload = buf
	return f, nil
}

// writeFrame writes one unmasked frame. A server never masks (RFC 6455 section 5.1).
func writeFrame(w *bufio.Writer, opcode byte, payload []byte) error {
	n := len(payload)
	hdr := []byte{0x80 | opcode}
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		return errors.New("payload too large for this fixture")
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}

func handleEcho(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		// Named rather than a bare 400: a spec that reaches here has proved the request
		// arrived and the upgrade header did not survive the path, which is a different
		// finding from a connection that never landed.
		http.Error(w, "ws-echo: this endpoint requires an Upgrade: websocket request", http.StatusBadRequest)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "ws-echo: no Sec-WebSocket-Key on an upgrade request", http.StatusBadRequest)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "ws-echo: the response writer cannot be hijacked", http.StatusInternalServerError)
		return
	}
	conn, brw, err := hijacker.Hijack()
	if err != nil {
		log.Printf("hijacking the connection: %v", err)
		return
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			log.Printf("closing an echo session: %v", cerr)
		}
	}()

	handshake := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey(key) + "\r\n\r\n"
	if _, werr := brw.WriteString(handshake); werr != nil {
		log.Printf("writing the handshake: %v", werr)
		return
	}
	if ferr := brw.Flush(); ferr != nil {
		log.Printf("flushing the handshake: %v", ferr)
		return
	}

	for {
		if derr := conn.SetReadDeadline(time.Now().Add(idleTimeout)); derr != nil {
			log.Printf("setting a read deadline: %v", derr)
			return
		}
		f, rerr := readFrame(brw.Reader)
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				log.Printf("reading a frame: %v", rerr)
			}
			return
		}
		if !f.fin || f.opcode == opContinuation {
			log.Printf("ws-echo received a fragmented frame, which this fixture does not carry")
			return
		}
		switch f.opcode {
		case opText, opBinary:
			if werr := writeFrame(brw.Writer, f.opcode, f.payload); werr != nil {
				log.Printf("echoing a frame: %v", werr)
				return
			}
		case opPing:
			if werr := writeFrame(brw.Writer, opPong, f.payload); werr != nil {
				log.Printf("answering a ping: %v", werr)
				return
			}
		case opPong:
			// Nothing to do; a peer may send one unsolicited.
		case opClose:
			if werr := writeFrame(brw.Writer, opClose, f.payload); werr != nil {
				log.Printf("echoing a close: %v", werr)
			}
			return
		default:
			log.Printf("ws-echo received opcode %#x, which this fixture does not carry", f.opcode)
			return
		}
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, "ws-echo\n"); err != nil {
		log.Printf("writing the plain-HTTP body for %s: %v", r.URL.Path, err)
	}
}

func main() {
	addr := os.Getenv("WS_ECHO_ADDR")
	if addr == "" {
		addr = ":8090"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/echo", handleEcho)
	mux.HandleFunc("/", handleRoot)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: it applies to a hijacked connection too, and would tear an echo
		// session down mid-exchange for no reason a test could interpret.
	}

	log.Printf("ws-echo listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("ws-echo: %v", err)
	}
}
