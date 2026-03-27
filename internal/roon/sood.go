package roon

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"time"

	"golang.org/x/net/ipv4"
)

const (
	soodPort         = 9003
	soodMulticast    = "239.255.90.90"
	soodMagic        = "SOOD"
	soodVersion      = '2'
	soodQuery        = 'Q'
	soodResponse     = 'R'
	discoveryTimeout = 5 * time.Second
)

// Discover scans the network for Roon Cores via SOOD protocol.
// Returns discovered cores within the timeout period.
func Discover(timeout time.Duration) ([]DiscoveredCore, error) {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", soodPort))
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}

	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		// Port might be in use, try ephemeral
		conn, err = net.ListenUDP("udp4", nil)
		if err != nil {
			return nil, fmt.Errorf("listen: %w", err)
		}
	}
	defer conn.Close()

	// Join multicast group
	p := ipv4.NewPacketConn(conn)
	multiAddr := net.ParseIP(soodMulticast)
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagMulticast != 0 && iface.Flags&net.FlagUp != 0 {
			p.JoinGroup(&iface, &net.UDPAddr{IP: multiAddr, Port: soodPort})
		}
	}

	// Send a query to solicit responses
	sendQuery(conn)

	conn.SetReadDeadline(time.Now().Add(timeout))

	seen := make(map[string]DiscoveredCore)
	buf := make([]byte, 4096)

	for {
		n, srcAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // timeout or error
		}

		core, err := parseSoodPacket(buf[:n], srcAddr)
		if err != nil {
			continue
		}

		if core.HTTPPort != "" {
			seen[core.UniqueID] = *core
		}
	}

	cores := make([]DiscoveredCore, 0, len(seen))
	for _, c := range seen {
		cores = append(cores, c)
	}
	return cores, nil
}

func sendQuery(conn *net.UDPConn) {
	tid := fmt.Sprintf("%08x", rand.Int31())
	pkt := buildSoodPacket(soodQuery, map[string]string{
		"_tid": tid,
	})

	// Send to multicast
	mcastAddr := &net.UDPAddr{IP: net.ParseIP(soodMulticast), Port: soodPort}
	conn.WriteToUDP(pkt, mcastAddr)

	// Send to broadcast
	bcastAddr := &net.UDPAddr{IP: net.IPv4bcast, Port: soodPort}
	conn.WriteToUDP(pkt, bcastAddr)
}

func buildSoodPacket(msgType byte, fields map[string]string) []byte {
	var pkt []byte
	pkt = append(pkt, []byte(soodMagic)...)
	pkt = append(pkt, byte(soodVersion))
	pkt = append(pkt, msgType)

	for k, v := range fields {
		pkt = append(pkt, k[0]) // type char = first byte of key
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(len(v)))
		pkt = append(pkt, lenBuf...)
		pkt = append(pkt, []byte(v)...)
	}
	return pkt
}

func parseSoodPacket(data []byte, src *net.UDPAddr) (*DiscoveredCore, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("packet too short")
	}
	if string(data[0:4]) != soodMagic {
		return nil, fmt.Errorf("bad magic")
	}
	if data[4] != byte(soodVersion) {
		return nil, fmt.Errorf("bad version")
	}
	if data[5] != byte(soodResponse) {
		return nil, fmt.Errorf("not a response")
	}

	fields := parseTLVFields(data[6:])

	core := &DiscoveredCore{
		IP: src.IP.String(),
	}

	for _, f := range fields {
		switch f.name {
		case "display_name", "name":
			core.DisplayName = f.value
		case "http_port":
			core.HTTPPort = f.value
		case "unique_id":
			core.UniqueID = f.value
		case "service_id":
			core.ServiceID = f.value
		}
	}

	if core.UniqueID == "" {
		core.UniqueID = core.IP + ":" + core.HTTPPort
	}

	return core, nil
}

type tlvField struct {
	typeChar byte
	name     string
	value    string
}

func parseTLVFields(data []byte) []tlvField {
	var fields []tlvField
	pos := 0

	// SOOD TLV: each field has a type string (null-terminated), then 2-byte length, then value
	// Based on node-roon-api sood.js parsing
	for pos < len(data) {
		// Read the field name (null-terminated string)
		nameStart := pos
		for pos < len(data) && data[pos] != 0 {
			pos++
		}
		if pos >= len(data) {
			break
		}
		name := string(data[nameStart:pos])
		pos++ // skip null terminator

		// Read 2-byte big-endian length
		if pos+2 > len(data) {
			break
		}
		length := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		pos += 2

		// Read value
		if pos+length > len(data) {
			break
		}
		value := string(data[pos : pos+length])
		pos += length

		fields = append(fields, tlvField{
			name:  name,
			value: value,
		})
	}

	return fields
}
