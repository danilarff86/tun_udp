package main

import (
	"log"
	"sync/atomic"
	"time"
)

var udpTunReceivedMgs int64
var udpTunProcessedMsg int64

var tunUdpReceivedMgs int64
var tunUdpProcessedMsg int64

func AddFromUDPToTunCounters(receivedMgs, processedMsg int) {
	atomic.AddInt64(&udpTunReceivedMgs, int64(receivedMgs))
	atomic.AddInt64(&udpTunProcessedMsg, int64(processedMsg))
}

func AddFromTunToUDPCounters(receivedMgs, processedMsg int) {
	atomic.AddInt64(&tunUdpReceivedMgs, int64(receivedMgs))
	atomic.AddInt64(&tunUdpProcessedMsg, int64(processedMsg))
}

func RunMonitor() {
	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-ticker.C:
			log.Printf("From TUN to UDP: %d  -  %d", atomic.SwapInt64(&tunUdpReceivedMgs, 0), atomic.SwapInt64(&tunUdpProcessedMsg, 0))
			log.Printf("From UDP to TUN: %d  -  %d", atomic.SwapInt64(&udpTunReceivedMgs, 0), atomic.SwapInt64(&udpTunProcessedMsg, 0))
		}

	}
}
