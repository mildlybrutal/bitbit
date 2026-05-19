package bitbit

import (
	"encoding/json"
	"math/rand"
	"net"
	"sync"
	"time"
)

type Piece []byte
type NodeID [20]byte

func NewNodeID() NodeID {
	var id NodeID
	rand.Read(id[:])
	return id
}

type Torrent struct {
	Pieces []byte
	Hashes []byte
}

type Peer struct {
	ID             string
	Bitfield       []bool
	NodeID         NodeID
	Addr           string
	Data           map[int][]byte
	Pieces         map[int]bool
	AmChoking      bool
	AmInterested   bool
	PeerChoking    bool
	PeerInterested bool
	SnubbedAt      time.Time
	BytesRecieved  int64
}

type ConnectionPool struct {
	mu          sync.Mutex
	Connections map[string]*PeerConnection
	MaxPeers    int
}

type Downloader struct {
	PeerID        string
	MissingPieces []int
	InProgress    map[int]bool
	PeerBitFields map[string][]bool //bitfields from connected peers
}

type PeerStats struct {
	Peer         *Peer
	DownloadRate float64
	UploadRate   float64

	LastDownloaded float64
	LastUploaded   float64
}

type Choker struct {
	Peers       []*PeerStats
	MaxUnchoked int
}

func RequestPieceUDP(fromAddr string, piece int) {
	addr, _ := net.ResolveUDPAddr("udp", fromAddr)
	conn, _ := net.DialUDP("udp", nil, addr)

	msg := Message{
		Type:  "REQUEST",
		Piece: piece,
	}

	data, _ := json.Marshal(msg)
	conn.Write(data)
}
