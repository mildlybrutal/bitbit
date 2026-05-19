package bitbit

import (
	"bytes"
	"sort"
)

type KV struct {
	Key   NodeID
	Value *Peer
}

type DHT struct {
	Self  *Peer
	Nodes []*Peer
	Data  map[string][]*Peer
}

func Distance(a, b NodeID) [20]byte {
	var d [20]byte
	for i := 0; i < 20; i++ {
		d[i] = a[i] ^ b[i]
	}
	return d
}

func (d *DHT) AddNode(p *Peer) {
	d.Nodes = append(d.Nodes, p)
}

func (d *DHT) FindClosest(target NodeID, k int) []*Peer {
	sort.Slice(d.Nodes, func(i, j int) bool {
		di := Distance(d.Nodes[i].NodeID, target)
		dj := Distance(d.Nodes[j].NodeID, target)

		return bytes.Compare(di[:], dj[:]) < 0
	})

	if len(d.Nodes) < k {
		return d.Nodes
	}

	return d.Nodes[:k]
}

func (d *DHT) Store(key string, p *Peer) {
	d.Data[key] = append(d.Data[key], p)
}

func (d *DHT) FindPeers(key string) []*Peer {
	return d.Data[key]
}

func (d *DHT) Lookup(target NodeID, k int) []*Peer {
	shortlist := d.FindClosest(target, k)
	visited := make(map[NodeID]bool)
	prevBest := shortlist[0]

	for {
		prevBest = shortlist[0]

		for _, node := range shortlist {
			if !visited[node.NodeID] {
				visited[node.NodeID] = true
				neighbors := d.FindClosest(node.NodeID, k)
				shortlist = append(shortlist, neighbors...)
			}
		}

		sort.Slice(shortlist, func(i, j int) bool {
			di := Distance(shortlist[i].NodeID, target)
			dj := Distance(shortlist[j].NodeID, target)
			return bytes.Compare(di[:], dj[:]) < 0
		})

		if len(shortlist) > k {
			shortlist = shortlist[:k]
		}

		if shortlist[0] == prevBest {
			break
		}
	}

	return shortlist
}
