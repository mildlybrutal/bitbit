package main

import (
	"fmt"
	"math/rand"
	"os"
)

func main() {
	torrent, infohash, _ := ParseTorrentFile("file.torrent")

	fmt.Printf("Name: %q, PieceLength: %d, Pieces: %d bytes\n",
		torrent.Info.Name, torrent.Info.PieceLength, len(torrent.Info.Pieces))

	rawFile, _ := os.ReadFile(torrent.Info.Name)

	rawFile, err := os.ReadFile(torrent.Info.Name)
	if err != nil {
		fmt.Println("ReadFile error:", err)
		return
	}
	fmt.Println("Raw file size:", len(rawFile))

	seed := &Peer{
		ID:     "seed",
		Pieces: map[int]bool{},
		Data:   SeedPieces(rawFile, torrent.Info.PieceLength),
	}

	for i := range seed.Data {
		seed.Pieces[i] = true
	}
	fmt.Println("Seed piece count:", len(seed.Data))

	dht := &DHT{
		Self:  seed,
		Nodes: []*Peer{},
		Data:  make(map[string][]*Peer),
	}

	leecher1 := &Peer{ID: "L1", Pieces: map[int]bool{}}
	leecher2 := &Peer{ID: "L2", Pieces: map[int]bool{}}
	key := string(infohash)
	dht.Store(key, seed)
	dht.Store(key, leecher1)
	dht.Store(key, leecher2)
	peers := dht.FindPeers(key)

	peerValues := make([]Peer, len(peers))
	for i, p := range peers {
		peerValues[i] = *p
	}

	peerStats := []*PeerStats{
		{Peer: seed, DownloadRate: 100},
		{Peer: leecher1, DownloadRate: 20},
		{Peer: leecher2, DownloadRate: 50},
	}

	go StartServer(seed, "8001")
	go StartServer(leecher1, "8002")
	go StartServer(leecher2, "8003")

	choker := &Choker{
		Peers:       peerStats,
		MaxUnchoked: 2,
	}
	hashes := SplitPieceHashes(torrent.Info.Pieces)
	totalPieces := len(hashes)
	subPieceCount := 3

	PrintState(peers)

	for round := 0; round < 5; round++ {
		fmt.Printf("\n=== Round %d ===\n", round)

		choker.ApplyWithOptimism()

		unchoked := make(map[*Peer]bool)

		for _, ps := range choker.SelectTopPeers() {
			unchoked[ps.Peer] = true
		}

		if opt := choker.OptimisticUnchoke(); opt != nil {
			unchoked[opt] = true
		}

		allowed := []*Peer{}
		for _, ps := range peerStats {
			if unchoked[ps.Peer] {
				allowed = append(allowed, ps.Peer)
			}
		}

		hasFirst := len(leecher1.Pieces) > 0
		piece := SelectNextPiece(peerValues, leecher1.Pieces, totalPieces, hasFirst)

		if piece == -1 {
			break
		}

		if len(allowed) > 0 {
			from := allowed[rand.Intn(len(allowed))]
			fmt.Printf("Downloading piece %d from %s\n", piece, from.ID)
			DownloadPiece(*from, piece, subPieceCount)

			pieceData := from.Data[piece]

			expectedHashes := hashes[piece]

			if VerifyPiece(pieceData, expectedHashes) {
				fmt.Printf("Piece %d verified\n", piece)
				leecher1.Pieces[piece] = true
			} else {
				fmt.Printf("Piece %d corruption\n", piece)
				return
			}

			var fromStats, toStats *PeerStats

			for _, ps := range peerStats {
				if ps.Peer == from {
					fromStats = ps
				}
				if ps.Peer == leecher1 {
					toStats = ps
				}
			}

			UpdateRates(fromStats, toStats, 10)
		}

		PrintState(peers)
		DecayRates(peerStats)
	}
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
