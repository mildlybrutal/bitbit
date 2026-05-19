package bitbit

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"net/url"
	"time"
)

const (
	udpConnectMagic   = 0x41727101980 // magic number required by spec
	udpActionConnect  = 0
	udpActionAnnounce = 1
)

func AnnounceUDP(announceURL string, req TrackerRequest) ([]PeerAddr, error) {
	// parse host:port from udp://host:port/announce
	host, err := parseUDPHost(announceURL)
	if err != nil {
		return nil, err
	}

	addr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		return nil, fmt.Errorf("resolving udp addr %s: %w", host, err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("dialing udp tracker: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(15 * time.Second))

	// step 1: connect request → get connection_id
	connectionID, err := udpConnect(conn)
	if err != nil {
		return nil, fmt.Errorf("udp connect: %w", err)
	}

	// step 2: announce request → get peers
	peers, err := udpAnnounce(conn, connectionID, req)
	if err != nil {
		return nil, fmt.Errorf("udp announce: %w", err)
	}

	return peers, nil
}

// udpConnect sends a connect request and returns the connection_id.
// Connect request: 16 bytes
//
//	8 bytes: magic constant 0x41727101980
//	4 bytes: action = 0 (connect)
//	4 bytes: transaction_id (random)
func udpConnect(conn *net.UDPConn) (uint64, error) {
	transactionID := randomUint32()

	req := make([]byte, 16)
	binary.BigEndian.PutUint64(req[0:8], udpConnectMagic)
	binary.BigEndian.PutUint32(req[8:12], udpActionConnect)
	binary.BigEndian.PutUint32(req[12:16], transactionID)

	if _, err := conn.Write(req); err != nil {
		return 0, fmt.Errorf("writing connect request: %w", err)
	}

	// connect response: 16 bytes
	//   4 bytes: action (must be 0)
	//   4 bytes: transaction_id (must match)
	//   8 bytes: connection_id
	resp := make([]byte, 16)
	if _, err := conn.Read(resp); err != nil {
		return 0, fmt.Errorf("reading connect response: %w", err)
	}

	action := binary.BigEndian.Uint32(resp[0:4])
	respTxID := binary.BigEndian.Uint32(resp[4:8])

	if action == 3 {
		return 0, fmt.Errorf("tracker error: %s", string(resp[8:]))
	}
	connectionID := binary.BigEndian.Uint64(resp[8:16])

	if action != udpActionConnect {
		return 0, fmt.Errorf("expected action 0, got %d", action)
	}
	if respTxID != transactionID {
		return 0, fmt.Errorf("transaction_id mismatch")
	}

	return connectionID, nil
}

// udpAnnounce sends an announce request and parses the peer list.
// Announce request: 98 bytes
//
//	 8 bytes: connection_id
//	 4 bytes: action = 1 (announce)
//	 4 bytes: transaction_id
//	20 bytes: info_hash
//	20 bytes: peer_id
//	 8 bytes: downloaded
//	 8 bytes: left
//	 8 bytes: uploaded
//	 4 bytes: event (0=none, 1=completed, 2=started, 3=stopped)
//	 4 bytes: IP (0 = use sender's IP)
//	 4 bytes: key (random)
//	 4 bytes: num_want (-1 = default)
//	 2 bytes: port
func udpAnnounce(conn *net.UDPConn, connectionID uint64, req TrackerRequest) ([]PeerAddr, error) {
	transactionID := randomUint32()

	buf := make([]byte, 98)
	binary.BigEndian.PutUint64(buf[0:8], connectionID)
	binary.BigEndian.PutUint32(buf[8:12], udpActionAnnounce)
	binary.BigEndian.PutUint32(buf[12:16], transactionID)
	copy(buf[16:36], req.InfoHash[:])
	copy(buf[36:56], req.PeerID[:])
	binary.BigEndian.PutUint64(buf[56:64], uint64(req.Downloaded))
	binary.BigEndian.PutUint64(buf[64:72], uint64(req.Left))
	binary.BigEndian.PutUint64(buf[72:80], uint64(req.Uploaded))
	binary.BigEndian.PutUint32(buf[80:84], 2)              // event=started
	binary.BigEndian.PutUint32(buf[84:88], 0)              // IP=default
	binary.BigEndian.PutUint32(buf[88:92], randomUint32()) // key
	binary.BigEndian.PutUint32(buf[92:96], ^uint32(0))     // num_want=-1
	binary.BigEndian.PutUint16(buf[96:98], uint16(req.Port))

	if _, err := conn.Write(buf); err != nil {
		return nil, fmt.Errorf("writing announce request: %w", err)
	}

	// announce response: minimum 20 bytes + 6 bytes per peer
	//   4 bytes: action (must be 1)
	//   4 bytes: transaction_id
	//   4 bytes: interval
	//   4 bytes: leechers
	//   4 bytes: seeders
	//   6 bytes * N: peers (compact format, same as HTTP)
	resp := make([]byte, 2048)
	n, err := conn.Read(resp)
	if err != nil {
		return nil, fmt.Errorf("reading announce response: %w", err)
	}
	if n < 20 {
		return nil, fmt.Errorf("announce response too short: %d bytes", n)
	}

	action := binary.BigEndian.Uint32(resp[0:4])
	respTxID := binary.BigEndian.Uint32(resp[4:8])

	if action == 3 {
		return nil, fmt.Errorf("tracker error: %s", string(resp[8:n]))
	}

	if action != udpActionAnnounce {
		return nil, fmt.Errorf("expected action 1, got %d", action)
	}
	if respTxID != transactionID {
		return nil, fmt.Errorf("transaction_id mismatch")
	}

	// peers start at byte 20
	return DecodeCompactPeers(resp[20:n])
}

func parseUDPHost(announceURL string) (string, error) {
	u, err := url.Parse(announceURL)
	if err != nil {
		return "", fmt.Errorf("invalid UDP tracker URL: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("could not parse UDP tracker URL: %s", announceURL)
	}
	return u.Host, nil
}

func randomUint32() uint32 {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(fmt.Sprintf("random read failed: %v", err))
	}
	return binary.BigEndian.Uint32(buf[:])
}
