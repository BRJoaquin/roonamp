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
	soodPort      = 9003
	soodMulticast = "239.255.90.90"
	soodMagic     = "SOOD"
	soodVersion   = 2
	soodQuery     = 'Q'
	soodResponse  = 'R'
	// roonServiceID identifies the Roon Core service; the Core only answers
	// SOOD queries whose query_service_id matches it.
	roonServiceID = "00720724-5143-4a9b-abac-0e50cba674bb"
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
			if len(seen) == 0 {
				// First core found: keep listening only briefly for
				// siblings instead of waiting out the full timeout.
				conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			}
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
		"query_service_id": roonServiceID,
		"_tid":             tid,
	})

	// Send to multicast
	mcastAddr := &net.UDPAddr{IP: net.ParseIP(soodMulticast), Port: soodPort}
	conn.WriteToUDP(pkt, mcastAddr)

	// Send to limited broadcast and each interface's directed broadcast
	conn.WriteToUDP(pkt, &net.UDPAddr{IP: net.IPv4bcast, Port: soodPort})
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagBroadcast == 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			bcast := make(net.IP, 4)
			ip4 := ipnet.IP.To4()
			for i := 0; i < 4; i++ {
				bcast[i] = ip4[i] | ^ipnet.Mask[i]
			}
			conn.WriteToUDP(pkt, &net.UDPAddr{IP: bcast, Port: soodPort})
		}
	}
}

func buildSoodPacket(msgType byte, fields map[string]string) []byte {
	var pkt []byte
	pkt = append(pkt, []byte(soodMagic)...)
	pkt = append(pkt, byte(soodVersion))
	pkt = append(pkt, msgType)

	// Each property: 1-byte name length, name, 2-byte BE value length, value.
	for k, v := range fields {
		pkt = append(pkt, byte(len(k)))
		pkt = append(pkt, []byte(k)...)
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

	if core.ServiceID != roonServiceID {
		return nil, fmt.Errorf("not a Roon Core response")
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

	// SOOD property encoding (node-roon-api sood.js): 1-byte name length,
	// name, 2-byte big-endian value length, value. A value length of 0xFFFF
	// means null.
	for pos < len(data) {
		nameLen := int(data[pos])
		pos++
		if nameLen == 0 || pos+nameLen > len(data) {
			break
		}
		name := string(data[pos : pos+nameLen])
		pos += nameLen

		if pos+2 > len(data) {
			break
		}
		length := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		pos += 2

		if length == 0xFFFF { // null value
			fields = append(fields, tlvField{name: name})
			continue
		}
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
