package main

import (
	"bytes"
	"crypto/sha1"
)

type Tracker struct {
	Swarm map[string][]*Peer
}

func (t *Tracker) Announce(torrentID string, p *Peer) {
	t.Swarm[torrentID] = append(t.Swarm[torrentID], p)

}

func (t *Tracker) GetPeers(torrentID string) []*Peer {
	return t.Swarm[torrentID]
}

func VerifyPiece(piece []byte, expectedHash []byte) bool {
	hash := sha1.Sum(piece)
	return bytes.Equal(hash[:], expectedHash)
}
