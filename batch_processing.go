package main

import (
	"log"
	"runtime"
	"sync/atomic"
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
				msgCount := int32(batch.msgCount)
				processedMsg := int32(0)
				time.AfterFunc(1*time.Millisecond, func() {
					AddFromUDPToTunCounters(msgCount, processedMsg)
				})
				if msgCount > 0 {
					for i := 0; i < batch.msgCount; i++ {
						message := batch.messages[i]
						bytes := message.Buffers[0]
						//log.Printf("Tun Interface Write: %+v\n", batch.msgCount)
						_, err := tunInterface.Write(bytes)
						if err != nil {
							log.Fatalf("Tun Interface Write: type unknown %+v\n", err)
						}
						processedMsg += 1
					}
					messagesPool.Put(batch)
				}
			}
		}
	}(pc)
}

func runUDPReadBatchThread(pc *PacketCollector) {
	go func(pc *PacketCollector) {
		runtime.LockOSThread()
		for {
			batch := messagesPool.Get().(*Batch)
			count, err := udpListenConn.ReadBatch(batch.messages, syscall.MSG_WAITFORONE)
			if err != nil {
				log.Fatalf("UDP Interface Read: type unknown %+v\n", err)
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
				msgCount := int32(batch.msgCount)
				processedMsg := int32(0)
				time.AfterFunc(1*time.Millisecond, func() {
					AddFromTunToUDPCounters(msgCount, atomic.LoadInt32(&processedMsg))
				})
				if msgCount > 0 {
					offset := int32(0)
					bucketSize := int32(0)
					for offset < msgCount {
						if (msgCount-offset)/int32(RCVR_MSG_PACK) >= 1 {
							bucketSize = offset + int32(RCVR_MSG_PACK)
						} else {
							bucketSize = offset + ((msgCount - offset) % int32(RCVR_MSG_PACK))
						}
						//log.Printf("offset: %d, count: %d, bucket: %d", offset, msgCount, bucketSize)
						n, err := udpWriterConn.WriteBatch(batch.messages[offset:bucketSize], syscall.MSG_WAITFORONE)
						if err != nil {
							log.Fatalf("UDP Interface Write error: %+v\n", err)
						}
						atomic.AddInt32(&processedMsg, int32(n))
						offset = bucketSize
					}
				}
			}
		}
	}(pc)
}
