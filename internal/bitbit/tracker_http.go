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
	q := u.Query()
	q.Set("info_hash", string(req.InfoHash[:]))
	q.Set("peer_id", string(req.PeerID[:]))
	q.Set("port", strconv.Itoa(req.Port))
	q.Set("uploaded", strconv.Itoa(req.Uploaded))
	q.Set("downloaded", strconv.Itoa(req.Downloaded))
	q.Set("left", strconv.Itoa(left))
	q.Set("compact", "1")

	u.RawQuery = q.Encode()
	return u.String(), err
}

func Announce(announceURL string, req TrackerRequest) ([]PeerAddr, error) {
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

	// tracker response is bencoded
	decoded, err := NewDecoder(bytes.NewReader(body)).Decode()
	if err != nil {
		return nil, fmt.Errorf("decoding tracker response: %w", err)
	}

	dict, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected tracker response format")
	}

	// tracker returned an error message
	if failureReason, ok := dict["failure reason"].(string); ok {
		return nil, fmt.Errorf("tracker error: %s", failureReason)
	}

	peersRaw, ok := dict["peers"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or non-compact peers field")
	}

	return DecodeCompactPeers([]byte(peersRaw))
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
