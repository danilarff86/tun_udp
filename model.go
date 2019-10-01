package main

import (
	"golang.org/x/net/ipv4"
	"net"
)

type DataPacket struct {
	data      []byte
	packetLen int
}

func (packet *DataPacket) ToMessage(address *net.UDPAddr) ipv4.Message {
	return ipv4.Message{
		OOB:     nil,
		Buffers: [][]byte{packet.data},
		N:       packet.packetLen,
		Addr:    address,
	}
}

type Batch struct {
	messages []ipv4.Message
	msgCount int
}
