package bitbit

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type TrackerRequest struct {
	InfoHash   [20]byte
	PeerID     [20]byte
	Port       int
	Uploaded   int
	Downloaded int
	Left       int
	Compact    int
}

type TrackerResponse struct {
	Interval int
	Peers    []PeerAddr
}

type PeerAddr struct {
	IP   net.IP
	Port uint16
}

func BuildAnnounceURL(announceURL string, req TrackerRequest, left int) (string, error) {
	u, err := url.Parse(announceURL)

	if err != nil {
		return "", fmt.Errorf("invalid announce URL: %w", err)
	}
	params := []string{
		"info_hash=" + escapeBinaryQuery(req.InfoHash[:]),
		"peer_id=" + escapeBinaryQuery(req.PeerID[:]),
		"port=" + strconv.Itoa(req.Port),
		"uploaded=" + strconv.Itoa(req.Uploaded),
		"downloaded=" + strconv.Itoa(req.Downloaded),
		"left=" + strconv.Itoa(left),
		"compact=1",
	}
	if u.RawQuery != "" {
		u.RawQuery += "&" + strings.Join(params, "&")
	} else {
		u.RawQuery = strings.Join(params, "&")
	}
	return u.String(), err
}

func escapeBinaryQuery(data []byte) string {
	var b strings.Builder
	for _, c := range data {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func Announce(announceURL string, req TrackerRequest) ([]PeerAddr, error) {
	if len(announceURL) >= 6 && announceURL[:6] == "udp://" {
		return AnnounceUDP(announceURL, req)
	}
	return AnnounceHTTP(announceURL, req)
}

func AnnounceHTTP(announceURL string, req TrackerRequest) ([]PeerAddr, error) {
	announceWithParams, err := BuildAnnounceURL(announceURL, req, req.Left)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(announceWithParams)
	if err != nil {
		return nil, fmt.Errorf("tracker request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading tracker response: %w", err)
	}
	decoded, err := NewDecoder(bytes.NewReader(body)).Decode()
	if err != nil {
		return nil, fmt.Errorf("decoding tracker response: %w", err)
	}
	dict, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected tracker response format")
	}
	if failureReason, ok := dict["failure reason"].(string); ok {
		return nil, fmt.Errorf("tracker error: %s", failureReason)
	}

	switch peersRaw := dict["peers"].(type) {
	case string:
		return DecodeCompactPeers([]byte(peersRaw))
	case []any:
		return decodePeerList(peersRaw)
	default:
		return nil, fmt.Errorf("missing or unsupported peers field")
	}
}

func decodePeerList(raw []any) ([]PeerAddr, error) {
	peers := make([]PeerAddr, 0, len(raw))
	for _, entry := range raw {
		dict, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid peer entry type %T", entry)
		}

		ipText, ok := dict["ip"].(string)
		if !ok {
			return nil, fmt.Errorf("peer entry missing ip")
		}
		portNum, ok := dict["port"].(int64)
		if !ok {
			return nil, fmt.Errorf("peer entry missing port")
		}

		ip := net.ParseIP(ipText)
		if ip == nil {
			return nil, fmt.Errorf("invalid peer ip %q", ipText)
		}
		peers = append(peers, PeerAddr{
			IP:   ip,
			Port: uint16(portNum),
		})
	}
	return peers, nil
}

// parses the compact peer format: 4 bytes IP + 2 bytes port, repeated
func DecodeCompactPeers(raw []byte) ([]PeerAddr, error) {
	if len(raw)%6 != 0 {
		return nil, fmt.Errorf("invalid compact peer list length: %d", len(raw))
	}

	peers := make([]PeerAddr, 0, len(raw)/6)
	for i := 0; i < len(raw); i += 6 {
		ip := net.IP(raw[i : i+4])
		port := binary.BigEndian.Uint16(raw[i+4 : i+6])
		peers = append(peers, PeerAddr{
			IP:   ip,
			Port: port,
		})
	}
	return peers, nil
}
