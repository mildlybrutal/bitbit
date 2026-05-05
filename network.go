package main

import (
	"encoding/json"
	"fmt"
	"net"
)

type Message struct {
	Type  string
	From  string
	Data  []byte
	Piece int
}

func StartServer(peer *Peer, port string) {
	addr, _ := net.ResolveUDPAddr("udp", ":"+port)

	conn, _ := net.ListenUDP("udp", addr)

	buf := make([]byte, 1024)

	for {
		n, remote, _ := conn.ReadFromUDP(buf)

		var msg Message

		json.Unmarshal(buf[:n], &msg)
		HandleMessage(peer, conn, remote, msg)
	}
}

func HandleMessage(p *Peer, conn *net.UDPConn, addr *net.UDPAddr, msg Message) {
	switch msg.Type {
	case "REQUEST":
		if p.Pieces[msg.Piece] {
			resp := Message{
				Type:  "DATA",
				From:  p.ID,
				Data:  p.Data[msg.Piece],
				Piece: msg.Piece,
			}
			send(conn, addr, resp)
		}

	case "DATA":
		p.Pieces[msg.Piece] = true
		p.Data[msg.Piece] = msg.Data
		fmt.Printf("%s received piece %d from %s\n", p.ID, msg.Piece, msg.From)
	}
}

func send(conn *net.UDPConn, addr *net.UDPAddr, msg Message) {
	data, _ := json.Marshal(msg)
	conn.WriteToUDP(data, addr)
}
