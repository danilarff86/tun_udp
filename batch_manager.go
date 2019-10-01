package main

import (
	"golang.org/x/net/ipv4"
	"runtime"
	"sync/atomic"
	"time"
)

func RunNewPacketCollector(bucketSize int) *PacketCollector {
	collector := &PacketCollector{
		atomicLock:  0,
		BucketSize:  bucketSize,
		activeBatch: &Batch{make([]ipv4.Message, 0), 0},
		dstChannel:  make(chan *Batch),
	}
	go collector.Run()
	return collector
}

type PacketCollector struct {
	atomicLock  int32
	BucketSize  int
	activeBatch *Batch
	dstChannel  chan *Batch
}

func (pc *PacketCollector) PushOne(message ipv4.Message) {
	for !pc.TryLock() {
		runtime.Gosched()
	}
	pc.activeBatch.msgCount = pc.activeBatch.msgCount + 1
	pc.activeBatch.messages = append(pc.activeBatch.messages, message)
	pc.Unlock()
}

func (pc *PacketCollector) Push(messages []ipv4.Message) {
	for !pc.TryLock() {
		runtime.Gosched()
	}
	pc.activeBatch.msgCount = pc.activeBatch.msgCount + len(messages)
	pc.activeBatch.messages = append(pc.activeBatch.messages, messages...)
	pc.Unlock()
}

func (pc *PacketCollector) Run() {
	ticker := time.NewTicker(1 * time.Millisecond)
	for {
		select {
		case <-ticker.C:
			pc.sendActiveBucket()
		}
	}
}

func (pc *PacketCollector) sendActiveBucket() {
	if pc.activeBatch.msgCount > 0 {
		for !pc.TryLock() {
			runtime.Gosched()
		}
		pc.dstChannel <- pc.activeBatch
		pc.activeBatch = &Batch{make([]ipv4.Message, 0), 0}
		pc.Unlock()
	}
}

func (pc *PacketCollector) TryLock() bool {
	return atomic.CompareAndSwapInt32(&pc.atomicLock, 0, 1)
}

func (pc *PacketCollector) Unlock() {
	atomic.CompareAndSwapInt32(&pc.atomicLock, 1, 0)
}

func getMessageBuffer(size int) []ipv4.Message {
	ms := make([]ipv4.Message, size)
	for i := 0; i < size; i++ {
		ms[i] = ipv4.Message{
			OOB:     make([]byte, 10),
			Buffers: [][]byte{make([]byte, RCVR_BUF_SIZE)},
		}
	}
	return ms
}
