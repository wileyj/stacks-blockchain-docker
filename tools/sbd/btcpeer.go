package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"
)

// A real Bitcoin P2P handshake for `sbd init` (owner, 2026-11-06: a TCP connect isn't enough): send a
// `version` message with the network's magic bytes and read the peer's `version`. That proves the
// address is a Bitcoin node on the right network, and its height shows whether it's in sync.

// btcMagic is the message start of the Bitcoin network each Stacks network follows. The Stacks testnet
// follows a Bitcoin regtest network (bitcoin.regtest.hiro.so:18444).
var btcMagic = map[string][4]byte{
	"mainnet": {0xf9, 0xbe, 0xb4, 0xd9},
	"testnet": {0xfa, 0xbf, 0xb5, 0xda},
}

// otherMagic names the networks a mismatched peer may be on.
var otherMagic = map[[4]byte]string{
	{0xf9, 0xbe, 0xb4, 0xd9}: "Bitcoin mainnet", {0xfa, 0xbf, 0xb5, 0xda}: "regtest",
	{0x0b, 0x11, 0x09, 0x07}: "testnet3", {0x1c, 0x16, 0x3f, 0x28}: "testnet4", {0x0a, 0x03, 0xcf, 0x40}: "signet",
}

// Probe outcomes, from what the peer did.
const (
	peerOK    = "ok"
	peerBusy  = "busy"  // accepted the connection, then closed it during the handshake
	peerWrong = "wrong" // answered, but not as a Bitcoin node of this network, or not at all
	peerDown  = "down"  // no connection (DNS, refused, timeout)
)

// classify turns a probe error into an outcome.
func classify(err error) string {
	switch {
	case err == nil:
		return peerOK
	// Closed (cleanly, or reset before reading our message) once connected: turned away, i.e. busy
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE):
		return peerBusy
	}
	var ne net.Error
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Op == "dial" {
		return peerDown
	}
	if errors.As(err, &ne) && ne.Timeout() {
		return peerWrong // connected, but no Bitcoin answer: typically the RPC port
	}
	return peerWrong
}

// BtcPeer is what the peer says about itself.
type BtcPeer struct {
	UserAgent string
	Height    int32
}

func checksum(payload []byte) []byte {
	a := sha256.Sum256(payload)
	b := sha256.Sum256(a[:])
	return b[:4]
}

func btcMessage(magic [4]byte, command string, payload []byte) []byte {
	var b bytes.Buffer
	b.Write(magic[:])
	cmd := make([]byte, 12)
	copy(cmd, command)
	b.Write(cmd)
	binary.Write(&b, binary.LittleEndian, uint32(len(payload)))
	b.Write(checksum(payload))
	b.Write(payload)
	return b.Bytes()
}

func versionPayload() []byte {
	var b bytes.Buffer
	le := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	le(int32(70016))           // protocol version
	le(uint64(0))              // services: none
	le(time.Now().Unix())      // timestamp
	b.Write(make([]byte, 26))  // addr_recv: services, IPv6 address, port
	b.Write(make([]byte, 26))  // addr_from
	nonce := make([]byte, 8)   //
	rand.Read(nonce)           //
	b.Write(nonce)             // nonce
	ua := "/sbd-init:0.1/"     //
	b.WriteByte(byte(len(ua))) // user agent (var_str)
	b.WriteString(ua)          //
	le(int32(0))               // start height
	b.WriteByte(0)             // relay: no
	return b.Bytes()
}

// ProbeBtcPeer performs the handshake against host:port for the Stacks network's Bitcoin network.
func ProbeBtcPeer(network, host, port string, timeout time.Duration) (BtcPeer, error) {
	magic, ok := btcMagic[network]
	if !ok {
		return BtcPeer{}, fmt.Errorf("no Bitcoin network known for %q", network)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return BtcPeer{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(btcMessage(magic, "version", versionPayload())); err != nil {
		return BtcPeer{}, err
	}
	// The peer answers with its own version (it may send verack or others first; skip those)
	for i := 0; i < 5; i++ {
		hdr := make([]byte, 24)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return BtcPeer{}, err
		}
		var got [4]byte
		copy(got[:], hdr[:4])
		if got != magic {
			if name, ok := otherMagic[got]; ok {
				return BtcPeer{}, fmt.Errorf("the peer is on %s, not the network %s follows", name, network)
			}
			return BtcPeer{}, errors.New("not a Bitcoin P2P port (unexpected reply)")
		}
		cmd := string(bytes.TrimRight(hdr[4:16], "\x00"))
		n := binary.LittleEndian.Uint32(hdr[16:20])
		if n > 1<<20 {
			return BtcPeer{}, errors.New("not a Bitcoin P2P port (oversized message)")
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return BtcPeer{}, err
		}
		if cmd == "version" {
			return parseBtcVersion(payload)
		}
	}
	return BtcPeer{}, errors.New("the peer never sent its version")
}

// parseBtcVersion reads the user agent and start height of a version payload.
func parseBtcVersion(p []byte) (BtcPeer, error) {
	const fixed = 4 + 8 + 8 + 26 + 26 + 8 // version, services, timestamp, two addresses, nonce
	if len(p) < fixed+1 {
		return BtcPeer{}, errors.New("short version message")
	}
	r := p[fixed:]
	n := int(r[0])
	if n >= 0xfd || len(r) < 1+n+4 {
		return BtcPeer{}, errors.New("malformed version message")
	}
	return BtcPeer{UserAgent: string(r[1 : 1+n]), Height: int32(binary.LittleEndian.Uint32(r[1+n:]))}, nil
}
