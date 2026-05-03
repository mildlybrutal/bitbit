package main

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"math"
	"math/rand"
	"sort"
)

func RarestFirst(peers []Peer, totalPieces int, myPieces map[int]bool) int {
	count := make([]int, totalPieces)
	for _, p := range peers {
		for piece := range p.Pieces {
			count[piece]++
		}
	}

	rarest, minCount := -1, math.MaxInt
	for piece, c := range count {
		if myPieces[piece] {
			continue
		}
		if c < minCount {
			minCount = c
			rarest = piece
		}
	}
	return rarest
}

func DownloadPiece(peer Peer, pieceIndex int, subPieceCount int) {
	for sub := 0; sub < subPieceCount; sub++ {
		fmt.Printf("Requesting sub-piece %d of piece %d from %s\n", sub, pieceIndex, peer.ID)
		// next sub-piece of THIS piece before moving on
	}
	fmt.Printf("Piece %d complete\n", pieceIndex)
}

func VerifyPiece(pieceData []byte, expectedHash []byte) bool {
	hash := sha1.Sum(pieceData)
	return bytes.Equal(hash[:], expectedHash)
}

func SeedPieces(data []byte, pieceSize int) map[int][]byte {
	pieces := make(map[int][]byte)

	index := 0
	for i := 0; i < len(data); i += pieceSize {
		end := i + pieceSize
		if end > len(data) {
			end = len(data)
		}

		pieces[index] = data[i:end]
		index++
	}

	return pieces
}

func SelectNextPiece(peers []Peer, myPieces map[int]bool, totalPieces int, hasFirst bool) int {
	if !hasFirst {
		return rand.Intn(totalPieces) // bootstrap: grab anything fast
	}
	return RarestFirst(peers, totalPieces, myPieces) // switch to rarest first after
}

func Endgame(peers []*Peer, missing []int) {
	for _, piece := range missing {
		for _, p := range peers {
			if p.Pieces[piece] {
				fmt.Printf("Broadcasting request for piece %d to %s\n", piece, p.ID)
				// fire request to ALL peers, not just one
			}
		}
	}
}

//Core Algorithm

// We sort the peers by download rate
func (c *Choker) SelectTopPeers() []*PeerStats {
	sort.Slice(c.Peers, func(i, j int) bool {
		return c.Peers[i].DownloadRate > c.Peers[j].DownloadRate
	})

	if len(c.Peers) < c.MaxUnchoked {
		return c.Peers
	}

	return c.Peers[:c.MaxUnchoked]
}

// Apply choke / unchoke
func (c *Choker) ApplyChoke() {
	topPeers := c.SelectTopPeers()
	unchoked := make(map[*Peer]bool)

	for _, ps := range topPeers {
		if unchoked[ps.Peer] {
			fmt.Printf("Unchoking %s\n", ps.Peer.ID)
		} else {
			fmt.Printf("Choking %s\n", ps.Peer.ID)
		}
	}
}

// Every ~30 sec, pick 1 random peer outside top set.
func (c *Choker) OptimisticUnchoke() *Peer {
	candidates := c.Peers

	if len(candidates) == 0 {
		return nil
	}

	r := rand.Intn(len(candidates))

	return candidates[r].Peer
}

func (c *Choker) ApplyWithOptimism() {
	topPeers := c.SelectTopPeers()

	optimistic := c.OptimisticUnchoke()

	unchoked := make(map[*Peer]bool)

	for _, ps := range topPeers {
		unchoked[ps.Peer] = true
	}

	if optimistic != nil {
		unchoked[optimistic] = true
		fmt.Printf("Optimistically unchoking %s\n", optimistic.ID)
	}

	for _, ps := range c.Peers {
		if unchoked[ps.Peer] {
			fmt.Printf("Unchoking %s\n", ps.Peer.ID)
		} else {
			fmt.Printf("Choking %s\n", ps.Peer.ID)
		}
	}
}

func UpdateRates(from *PeerStats, to *PeerStats, amount float64) {
	from.UploadRate += amount
	from.LastUploaded = amount

	to.DownloadRate += amount
	to.LastDownloaded = amount
}

func DecayRates(stats []*PeerStats) {
	for _, ps := range stats {
		ps.DownloadRate *= 0.8
		ps.UploadRate *= 0.8
	}
}
