package bitbit

import (
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	MaxPipelined = 5
	MaxWorkers   = 30
)

type Block struct {
	PieceIndex int
	Offset     int
	Length     int
	Data       []byte
	Done       bool
}

type PieceWork struct {
	Index  int
	Hash   [20]byte
	Length int
	Blocks []Block
	Done   bool
}

type DownloadManager struct {
	Torrent     *TorrentFile
	InfoHash    [20]byte
	PeerID      [20]byte
	Peers       []PeerAddr
	WorkQueue   chan *PieceWork //Pieces to download
	ResultQueue chan *PieceWork //Pieces that have been downloaded, verify pieces ready to write
	MyPieces    map[int]bool
	TotalPieces int
}

func (dm *DownloadManager) Start(fw *FileWriter) error {
	hashes := SplitPieceHashes(dm.Torrent.Info.Pieces)
	dm.TotalPieces = len(hashes)
	dm.WorkQueue = make(chan *PieceWork, dm.TotalPieces)
	dm.ResultQueue = make(chan *PieceWork, dm.TotalPieces)

	for i, hash := range hashes {
		if dm.MyPieces[i] {
			continue
		}
		pieceLen := dm.Torrent.Info.PieceLength

		if i == dm.TotalPieces-1 {
			pieceLen = dm.Torrent.Info.Length - (dm.TotalPieces-1)*dm.Torrent.Info.PieceLength
		}
		var h [20]byte
		copy(h[:], hash)

		dm.WorkQueue <- &PieceWork{
			Index:  i,
			Hash:   h,
			Length: pieceLen,
		}
	}

	workers := len(dm.Peers)

	if workers > MaxWorkers {
		workers = MaxWorkers
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(addr PeerAddr) {
			defer wg.Done()
			dm.workerLoop(addr)
		}(dm.Peers[i])
	}

	go func() {
		wg.Wait()
		close(dm.ResultQueue)
	}()

	for pw := range dm.ResultQueue {
		data := assembleBlocks(pw)

		if err := fw.WritePiece(pw.Index, data); err != nil {
			return fmt.Errorf("writing piece %d: %w", pw.Index, err)
		}
		dm.MyPieces[pw.Index] = true
		log.Printf("piece %d/%d done\n", pw.Index+1, dm.TotalPieces)
	}

	if len(dm.WorkQueue) > 0 {
		return fmt.Errorf("%d pieces failed to download", len(dm.WorkQueue))
	}
	return fw.Close()
}

//connects to one peer and pulls pieces from workqueue until empty

func (dm *DownloadManager) workerLoop(addr PeerAddr) {
	pc, err := Dial(addr, dm.InfoHash, dm.PeerID)
	if err != nil {
		log.Printf("dial %s:%d failed: %v\n", addr.IP, addr.Port, err)
		return
	}

	defer pc.Conn.Close()

	if err := pc.Handshake(); err != nil {
		log.Printf("handshake %s:%d failed: %v\n", addr.IP, addr.Port, err)
		return
	}

	if err := pc.ReadBitfield(dm.TotalPieces); err != nil {
		log.Printf("bitfield %s:%d: %v (continuing)\n", addr.IP, addr.Port, err)
	}

	if err := pc.SendInterested(); err != nil {
		return
	}

	if err := waitForUnchoke(pc); err != nil {
		log.Printf("unchoke wait %s:%d: %v\n", addr.IP, addr.Port, err)
		return
	}

	pc.PeerChoking = false
	for {
		var pw *PieceWork
		select {
		case pw = <-dm.WorkQueue:
		default:
			return
		}

		if !pc.hasPiece(pw.Index) {
			dm.WorkQueue <- pw
			time.Sleep(50 * time.Millisecond)
			continue
		}

		if err := dm.downloadPiece(pc, pw); err != nil {
			log.Printf("piece %d from %s failed: %v — requeueing\n", pw.Index, addr.IP, err)
			dm.WorkQueue <- pw
			continue
		}
		dm.ResultQueue <- pw
	}
}

func waitForUnchoke(pc *PeerConnection) error {
	pc.Conn.SetDeadline(time.Now().Add(30 * time.Second))
	defer pc.Conn.SetDeadline(time.Time{})

	for {
		msg, err := pc.ReceiveMessage()
		if err != nil {
			return err
		}
		switch msg.ID {
		case MsgUnchoke:
			return nil
		case MsgChoke:
			return fmt.Errorf("peer choked us immediately")
		case MsgHave, MsgBitfield:
			continue
		}
	}
}

func (dm *DownloadManager) downloadPiece(pc *PeerConnection, pw *PieceWork) error {
	pw.Blocks = buildBlocks(pw)

	requested := 0
	recieved := 0

	pc.Conn.SetDeadline(time.Now().Add(30 * time.Second))
	defer pc.Conn.SetDeadline(time.Time{})

	for recieved < len(pw.Blocks) {
		for requested < len(pw.Blocks) && requested-recieved < MaxPipelined {
			b := pw.Blocks[requested]
			err := pc.SendRequest(BlockRequest{
				PieceIndex:  uint32(pw.Index),
				BlockOffset: uint32(b.Offset),
				BlockLength: uint32(b.Length),
			})
			if err != nil {
				return fmt.Errorf("send request block %d: %w", requested, err)

			}
			requested++
		}

		msg, err := pc.ReceiveMessage()
		if err != nil {
			return fmt.Errorf("receive block: %w", err)
		}

		switch msg.ID {
		case MsgPiece:
			_, offset, data, err := ParsePieceMessage(msg.Payload)
			if err != nil {
				return err
			}
			blockIdx := int(offset) / BlockSize
			if blockIdx >= len(pw.Blocks) {
				return fmt.Errorf("block index %d out of range", blockIdx)
			}
			pw.Blocks[blockIdx].Data = data
			pw.Blocks[blockIdx].Done = true
			recieved++
			pc.Conn.SetDeadline(time.Now().Add(30 * time.Second))
		case MsgChoke:
			return fmt.Errorf("peer choked mid-download")
		case MsgHave:
			// peer got a new piece while we were downloading — update bitfield
			if len(msg.Payload) == 4 {
				idx := int(msg.Payload[0])<<24 | int(msg.Payload[1])<<16 | int(msg.Payload[2])<<8 | int(msg.Payload[3])
				if idx < len(pc.Bitfield) {
					pc.Bitfield[idx] = true
				}
			}
		}

	}
	data := assembleBlocks(pw)
	if !VerifyPiece(data, pw.Hash[:]) {
		return fmt.Errorf("piece %d hash mismatch", pw.Index)
	}
	pw.Done = true
	return nil
}

func buildBlocks(pw *PieceWork) []Block {
	var blocks []Block
	for offset := 0; offset < pw.Length; offset += BlockSize {
		length := BlockSize
		if offset+length > pw.Length {
			length = pw.Length - offset
		}
		blocks = append(blocks, Block{
			PieceIndex: pw.Index,
			Offset:     offset,
			Length:     length,
		})
	}
	return blocks
}

func assembleBlocks(pw *PieceWork) []byte {
	buf := make([]byte, pw.Length)
	for _, b := range pw.Blocks {
		copy(buf[b.Offset:], b.Data)
	}
	return buf
}
func (pc *PeerConnection) hasPiece(index int) bool {
	if index >= len(pc.Bitfield) {
		return false
	}
	return pc.Bitfield[index]
}
