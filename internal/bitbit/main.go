package bitbit

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"strings"
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
	log.Printf("trackers:     %d", len(torrent.AnnounceList))
	log.Printf("web seeds:    %d", len(torrent.URLList))

	// 2. generate a random peer ID
	// convention: "-<client><version>-<12 random bytes>"
	var peerID [20]byte
	copy(peerID[:8], []byte("-BB0001-"))
	if _, err := rand.Read(peerID[8:]); err != nil {
		return fmt.Errorf("generating peer ID: %w", err)
	}

	// 3. set up file writer
	fw, err := NewFileWriter(torrent)
	if err != nil {
		return fmt.Errorf("file writer: %w", err)
	}

	announceURLs := supportedAnnounceURLs(torrent.AnnounceList)
	if len(announceURLs) == 0 {
		if len(torrent.URLList) == 0 {
			return fmt.Errorf("torrent has neither announce URL nor web seeds")
		}

		log.Println("downloading from web seeds...")
		if err := DownloadFromWebSeeds(torrent, fw); err != nil {
			return fmt.Errorf("web seed download failed: %w", err)
		}

		log.Printf("done. saved to %s", torrent.Info.Name)
		return nil
	}

	// 4. announce to tracker, get peer list
	log.Println("announcing to tracker...")
	req := TrackerRequest{
		InfoHash:   infoHashFixed,
		PeerID:     peerID,
		Port:       6881,
		Uploaded:   0,
		Downloaded: 0,
		Left:       torrent.Info.Length,
		Compact:    1,
	}

	peers, trackerURL, trackerErr := announceToTrackers(announceURLs, req)
	if trackerErr != nil {
		if len(torrent.URLList) > 0 {
			log.Printf("tracker announce failed (%v), falling back to web seeds", trackerErr)
			if err := DownloadFromWebSeeds(torrent, fw); err != nil {
				return fmt.Errorf("tracker announce: %v; web seed fallback failed: %w", trackerErr, err)
			}
			log.Printf("done. saved to %s", torrent.Info.Name)
			return nil
		}
		return fmt.Errorf("tracker announce: %w", trackerErr)
	}
	log.Printf("using tracker: %s", trackerURL)
	log.Printf("got %d peers from tracker", len(peers))

	if len(peers) == 0 {
		if len(torrent.URLList) > 0 {
			log.Println("no peers returned by tracker, falling back to web seeds")
			if err := DownloadFromWebSeeds(torrent, fw); err != nil {
				return fmt.Errorf("no peers returned by tracker; web seed fallback failed: %w", err)
			}
			log.Printf("done. saved to %s", torrent.Info.Name)
			return nil
		}
		return fmt.Errorf("no peers returned by tracker, nothing to do")
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

func supportedAnnounceURLs(urls []string) []string {
	var supported []string
	for _, raw := range urls {
		switch {
		case strings.HasPrefix(raw, "udp://"), strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
			supported = append(supported, raw)
		}
	}
	return supported
}

func announceToTrackers(urls []string, req TrackerRequest) ([]PeerAddr, string, error) {
	var errs []string
	for _, trackerURL := range urls {
		peers, err := Announce(trackerURL, req)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", trackerURL, err))
			continue
		}
		return peers, trackerURL, nil
	}
	return nil, "", errors.New(strings.Join(errs, "; "))
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
