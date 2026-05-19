package bitbit

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

type PeerConnection struct {
	Conn           net.Conn
	PeerID         [20]byte
	InfoHash       [20]byte
	AmChoking      bool
	AmInterested   bool
	PeerChoking    bool
	PeerInterested bool
	Bitfield       []bool
}

type WireMessage struct {
	ID      uint8
	Payload []byte
}

const (
	MsgChoke         = 0
	MsgUnchoke       = 1
	MsgInterested    = 2
	MsgNotInterested = 3
	MsgHave          = 4
	MsgBitfield      = 5
	MsgRequest       = 6
	MsgPiece         = 7
	MsgCancel        = 8
)

type BlockRequest struct {
	PieceIndex  uint32
	BlockOffset uint32
	BlockLength uint32 // always 16KB (16384) except last block
}

const (
	ProtocolString = "BitTorrent protocol"
	BlockSize      = 16384 // 16KB
)

// Handshake sends and receives the BT handshake.
// Format: <pstrlen><pstr><reserved><info_hash><peer_id>
// pstrlen = 19, pstr = "BitTorrent protocol", reserved = 8 zero bytes
func (pc *PeerConnection) Handshake() error {
	buf := make([]byte, 0, 68)
	buf = append(buf, byte(len(ProtocolString)))
	buf = append(buf, []byte(ProtocolString)...)
	buf = append(buf, make([]byte, 8)...)
	buf = append(buf, pc.InfoHash[:]...)
	buf = append(buf, pc.PeerID[:]...)

	pc.Conn.SetDeadline(time.Now().Add(10 * time.Second))
	defer pc.Conn.SetDeadline(time.Time{})

	if _, err := pc.Conn.Write(buf); err != nil {
		return fmt.Errorf("handshake write: %w", err)
	}

	resp := make([]byte, 68)
	if _, err := io.ReadFull(pc.Conn, resp); err != nil {
		return fmt.Errorf("handshake read: %w", err)
	}

	pstrlen := int(resp[0])
	if pstrlen != len(ProtocolString) {
		return fmt.Errorf("unexpected pstrlen: %d", pstrlen)
	}

	pstr := string(resp[1 : 1+pstrlen])
	if pstr != ProtocolString {
		return fmt.Errorf("unexpected protocol: %s", pstr)
	}

	// resp[20:28] = reserved bytes (extensions, ignore for now)
	var theirInfoHash [20]byte
	copy(theirInfoHash[:], resp[28:48])
	if theirInfoHash != pc.InfoHash {
		return fmt.Errorf("info_hash mismatch")
	}

	copy(pc.PeerID[:], resp[48:68])
	return nil
}

// SendMessage writes a length-prefixed wire message.
// Wire format: [4-byte big-endian length][1-byte msg ID][payload]
func (pc *PeerConnection) SendMessage(msg WireMessage) error {
	length := uint32(1 + len(msg.Payload)) // 1 for ID byte

	buf := make([]byte, 4+1+len(msg.Payload))
	binary.BigEndian.PutUint32(buf[0:4], length)
	buf[4] = msg.ID
	copy(buf[5:], msg.Payload)

	_, err := pc.Conn.Write(buf)
	return err
}

// ReceiveMessage reads one length-prefixed wire message.
func (pc *PeerConnection) ReceiveMessage() (WireMessage, error) {
	var msg WireMessage

	// read 4-byte length prefix
	var lengthBuf [4]byte
	if _, err := io.ReadFull(pc.Conn, lengthBuf[:]); err != nil {
		return msg, fmt.Errorf("reading length prefix: %w", err)
	}

	length := binary.BigEndian.Uint32(lengthBuf[:])

	// keepalive: length == 0, no ID or payload
	if length == 0 {
		return msg, nil
	}

	// read ID byte
	var idBuf [1]byte
	if _, err := io.ReadFull(pc.Conn, idBuf[:]); err != nil {
		return msg, fmt.Errorf("reading msg ID: %w", err)
	}
	msg.ID = idBuf[0]

	// read payload (length - 1 because ID byte is included in length)
	if length > 1 {
		msg.Payload = make([]byte, length-1)
		if _, err := io.ReadFull(pc.Conn, msg.Payload); err != nil {
			return msg, fmt.Errorf("reading payload: %w", err)
		}
	}

	return msg, nil
}

// SendRequest sends a REQUEST message for a block.
func (pc *PeerConnection) SendRequest(req BlockRequest) error {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], req.PieceIndex)
	binary.BigEndian.PutUint32(payload[4:8], req.BlockOffset)
	binary.BigEndian.PutUint32(payload[8:12], req.BlockLength)

	return pc.SendMessage(WireMessage{ID: MsgRequest, Payload: payload})
}

// SendInterested sends the INTERESTED message.
func (pc *PeerConnection) SendInterested() error {
	return pc.SendMessage(WireMessage{ID: MsgInterested})
}

// SendUnchoke sends the UNCHOKE message.
func (pc *PeerConnection) SendUnchoke() error {
	return pc.SendMessage(WireMessage{ID: MsgUnchoke})
}

// ReadBitfield reads a BITFIELD message and populates pc.Bitfield.
// Must be called right after handshake — bitfield is the first message peers send.
func (pc *PeerConnection) ReadBitfield(totalPieces int) error {
	pc.Conn.SetDeadline(time.Now().Add(5 * time.Second))
	defer pc.Conn.SetDeadline(time.Time{})

	msg, err := pc.ReceiveMessage()
	if err != nil {
		return fmt.Errorf("reading bitfield message: %w", err)
	}
	if msg.ID != MsgBitfield {
		return fmt.Errorf("expected bitfield, got msg ID %d", msg.ID)
	}

	pc.Bitfield = make([]bool, totalPieces)
	for i := 0; i < totalPieces; i++ {
		byteIdx := i / 8
		bitIdx := 7 - (i % 8) // MSB first
		if byteIdx < len(msg.Payload) {
			pc.Bitfield[i] = (msg.Payload[byteIdx]>>bitIdx)&1 == 1
		}
	}
	return nil
}

// ParsePieceMessage extracts index, offset, and block data from a PIECE message payload.
func ParsePieceMessage(payload []byte) (pieceIndex uint32, blockOffset uint32, data []byte, err error) {
	if len(payload) < 8 {
		return 0, 0, nil, fmt.Errorf("piece payload too short: %d bytes", len(payload))
	}
	pieceIndex = binary.BigEndian.Uint32(payload[0:4])
	blockOffset = binary.BigEndian.Uint32(payload[4:8])
	data = payload[8:]
	return
}

// Dial opens a TCP connection to a peer and returns a PeerConnection ready for handshake.
func Dial(addr PeerAddr, infoHash [20]byte, peerID [20]byte) (*PeerConnection, error) {
	connStr := fmt.Sprintf("%s: %d", addr.IP.String(), addr.Port)
	conn, err := net.DialTimeout("tcp", connStr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", connStr, err)
	}
	return &PeerConnection{
		Conn:        conn,
		InfoHash:    infoHash,
		PeerID:      peerID,
		AmChoking:   true, // we start choking per spec
		PeerChoking: true, // assume peer starts choking us
	}, nil
}
