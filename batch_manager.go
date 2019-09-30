package main

import (
	"golang.org/x/net/ipv4"
	"sync"
	"sync/atomic"
	"time"
)

func NewPacketCollector(bucketSize int, dstChannel chan *Batch) *PacketCollector {
	batchPool := &sync.Pool{
		New: func() interface{} {
			return &Batch{getMessageBuffer(bucketSize), bucketSize}
		},
	}
	return &PacketCollector{
		atomicLock:  0,
		BucketSize:  bucketSize,
		batchPool:   batchPool,
		activeBatch: batchPool.Get().(*Batch),
		dstChannel:  dstChannel,
	}
}

type PacketCollector struct {
	atomicLock  int32
	BucketSize  int
	batchPool   *sync.Pool
	activeBatch *Batch
	dstChannel  chan *Batch
}

func (pc *PacketCollector) Push(data []ipv4.Message) {
	for !pc.TryLock() {
		pc.activeBatch.msgCount = pc.activeBatch.msgCount + len(data)
		pc.activeBatch.messages = append(pc.activeBatch.messages, data...)
	}
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
	for !pc.TryLock() {
		pc.dstChannel <- pc.activeBatch
		pc.activeBatch = pc.batchPool.Get().(*Batch)
	}
	pc.Unlock()
}

func (pc *PacketCollector) TryLock() bool {
	return atomic.CompareAndSwapInt32(&pc.atomicLock, 0, 1)
}

func (pc *PacketCollector) Unlock() {
	atomic.CompareAndSwapInt32(&pc.atomicLock, 1, 0)
}
