package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakePeer answers one handshake with magic and a version carrying ua and height.
func fakePeer(t *testing.T, magic [4]byte, ua string, height int32, sendVerackFirst bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		hdr := make([]byte, 24)
		io.ReadFull(c, hdr)
		io.ReadFull(c, make([]byte, binary.LittleEndian.Uint32(hdr[16:20])))
		if sendVerackFirst {
			c.Write(btcMessage(magic, "verack", nil))
		}
		var p bytes.Buffer
		p.Write(make([]byte, 4+8+8+26+26+8))
		p.WriteByte(byte(len(ua)))
		p.WriteString(ua)
		binary.Write(&p, binary.LittleEndian, height)
		p.WriteByte(1)
		c.Write(btcMessage(magic, "version", p.Bytes()))
	}()
	return ln.Addr().String()
}

func TestProbeBtcPeer(t *testing.T) {
	addr := fakePeer(t, btcMagic["mainnet"], "/Satoshi:28.1.0/", 974812, true)
	host, port, _ := net.SplitHostPort(addr)
	peer, err := ProbeBtcPeer("mainnet", host, port, 3*time.Second)
	if err != nil || peer.UserAgent != "/Satoshi:28.1.0/" || peer.Height != 974812 {
		t.Errorf("got %+v, %v", peer, err)
	}

	addr = fakePeer(t, [4]byte{0x0b, 0x11, 0x09, 0x07}, "/Satoshi:28.1.0/", 1, false)
	host, port, _ = net.SplitHostPort(addr)
	if _, err := ProbeBtcPeer("mainnet", host, port, 3*time.Second); err == nil || !strings.Contains(err.Error(), "testnet3") {
		t.Errorf("a peer on another network must be named: %v", err)
	}

	// Something that isn't Bitcoin: answers HTTP
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n                 "))
			c.Close()
		}
	}()
	host, port, _ = net.SplitHostPort(ln.Addr().String())
	if _, err := ProbeBtcPeer("mainnet", host, port, 3*time.Second); err == nil || !strings.Contains(err.Error(), "not a Bitcoin P2P port") {
		t.Errorf("a non-Bitcoin port must be recognised: %v", err)
	}
}
