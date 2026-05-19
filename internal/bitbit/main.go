package bitbit

import (
	"crypto/rand"
	"fmt"
	"log"
)

func Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: bitbit <file.torrent>")
	}

	torrentPath := args[0]

	// 1. parse torrent file
	torrent, infoHash, err := ParseTorrentFile(torrentPath)
	if err != nil {
		return fmt.Errorf("parse torrent: %w", err)
	}

	var infoHashFixed [20]byte
	copy(infoHashFixed[:], infoHash)

	log.Printf("name:         %s", torrent.Info.Name)
	log.Printf("piece length: %d bytes", torrent.Info.PieceLength)
	log.Printf("total length: %d bytes", torrent.Info.Length)
	log.Printf("pieces:       %d", len(torrent.Info.Pieces)/20)
	log.Printf("announce:     %s", torrent.Announcer)

	// 2. generate a random peer ID
	// convention: "-<client><version>-<12 random bytes>"
	var peerID [20]byte
	copy(peerID[:8], []byte("-BB0001-"))
	if _, err := rand.Read(peerID[8:]); err != nil {
		return fmt.Errorf("generating peer ID: %w", err)
	}

	// 3. announce to tracker, get peer list
	log.Println("announcing to tracker...")
	peers, err := Announce(torrent.Announcer, TrackerRequest{
		InfoHash:   infoHashFixed,
		PeerID:     peerID,
		Port:       6881,
		Uploaded:   0,
		Downloaded: 0,
		Left:       torrent.Info.Length,
		Compact:    1,
	})
	if err != nil {
		return fmt.Errorf("tracker announce: %w", err)
	}
	log.Printf("got %d peers from tracker", len(peers))

	if len(peers) == 0 {
		return fmt.Errorf("no peers returned by tracker, nothing to do")
	}

	// 4. set up file writer
	fw, err := NewFileWriter(torrent.Info.Name, torrent.Info.Length, torrent.Info.PieceLength)
	if err != nil {
		return fmt.Errorf("file writer: %w", err)
	}

	// 5. set up and start download manager
	dm := &DownloadManager{
		Torrent:     torrent,
		InfoHash:    infoHashFixed,
		PeerID:      peerID,
		Peers:       peers,
		MyPieces:    make(map[int]bool),
		TotalPieces: len(torrent.Info.Pieces) / 20,
	}

	log.Println("starting download...")
	if err := dm.Start(fw); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	log.Printf("done. saved to %s", torrent.Info.Name)
	return nil
}

func PrintState(peers []*Peer) {
	for _, p := range peers {
		fmt.Printf("%s has pieces: ", p.ID)
		for k := range p.Pieces {
			fmt.Printf("%d ", k)
		}
		fmt.Println()
	}
}
