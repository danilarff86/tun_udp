package main

import (
	"log"
	"runtime"
	"syscall"
	"time"
)

func runTunReadBatchThread(pc *PacketCollector) {
	go func(pc *PacketCollector) {
		runtime.LockOSThread()
		var packet = make([]byte, mtu)
		for {
			plen, err := tunInterface.Read(packet)
			if err != nil {
				log.Fatalf("Tun Interface Read: type unknown %+v\n", err)
			}
			dataPacket := getDataPacket()
			copy(dataPacket.data, packet[:plen])
			dataPacket.packetLen = plen
			pc.PushOne(dataPacket.ToMessage(dstIP))
		}
	}(pc)
}

func runTunWriteBatchThread(pc *PacketCollector) {
	go func(pc *PacketCollector) {
		runtime.LockOSThread()
		for {
			select {
			case batch := <-pc.dstChannel:
				time.AfterFunc(1*time.Millisecond, func() {
				})
				for i := 0; i < batch.msgCount; i++ {
					message := batch.messages[i]
					bytes := message.Buffers[0]
					//log.Printf("Tun Interface Write: %+v\n", batch.msgCount)
					_, err := tunInterface.Write(bytes)
					if err != nil {
						log.Fatalf("Tun Interface Write: type unknown %+v\n", err)
					}
				}
				messagesPool.Put(batch)
			}
		}
	}(pc)
}

func runUDPReadBatchThread(pc *PacketCollector) {
	go func(pc *PacketCollector) {
		runtime.LockOSThread()
		//packet := make([]byte, mtu)
		for {
			batch := messagesPool.Get().(*Batch)
			count, err := udpListenConn.ReadBatch(batch.messages, syscall.MSG_WAITFORONE)
			if err != nil {
				log.Fatal("UDP Interface Read: type unknown %+v\n", err)
			}
			batch.msgCount = count
			pc.Push(batch.messages[:count])
		}
	}(pc)
}

func runUDPBatchWriteThread(pc *PacketCollector) {
	go func(pc *PacketCollector) {
		runtime.LockOSThread()
		for {
			select {
			case batch := <-pc.dstChannel:
				//log.Printf("Messages count %+v", batch)
				_, err := udpWriterConn.WriteBatch(batch.messages, 0)
				if err != nil {
					log.Fatalf("UDP Interface Write error: %+v\n", err)
				}
			}
		}
	}(pc)
}
