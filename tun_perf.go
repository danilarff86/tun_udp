package main

import (
	"encoding/binary"
	"fmt"
	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
	"log"
	"net"
	"sync"
)

const (
	tunName1 string = "tun1"
	tunName2 string = "tun2"

	txQLen int = 100
	mtu    int = 1500

	protocolOffset         int = 9
	srcIPOffset            int = 3 * 4
	dstIPOffset            int = 4 * 4
	ipHeaderLength         int = 20
	ipHeaderChecksumOffset int = 10
	tcpChecksumOffset      int = 16
	udpChecksumOffset      int = 6
)

type DataPacket struct {
	data      []byte
	packetLen int
}

var tunIP1 net.IP = []byte{10, 0, 1, 10}
var tunIP2 net.IP = []byte{10, 0, 2, 10}

var packetPool *sync.Pool

func init() {
	packetPool = &sync.Pool{
		New: func() interface{} {
			return &DataPacket{data: make([]byte, mtu), packetLen: 0}
		},
	}
}

func getDataPacket() *DataPacket {
	return packetPool.Get().(*DataPacket)
}

func putDataPacket(p *DataPacket) {
	packetPool.Put(p)
}

func checksum(bytes []byte) uint16 {
	// Clear checksum bytes
	bytes[10] = 0
	bytes[11] = 0

	// Compute checksum
	var csum uint32
	for i := 0; i < len(bytes); i += 2 {
		csum += uint32(bytes[i]) << 8
		csum += uint32(bytes[i+1])
	}
	for {
		// Break when sum is less or equals to 0xFFFF
		if csum <= 65535 {
			break
		}
		// Add carry to the sum
		csum = (csum >> 16) + uint32(uint16(csum))
	}
	// Flip all the bits
	return ^uint16(csum)
}

func createTun(ip net.IP, tunName string) *water.Interface {
	var tunNetwork = &net.IPNet{IP: ip, Mask: []byte{255, 255, 255, 0}}

	var config = water.Config{
		DeviceType: water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name: tunName,
		},
	}

	tunInterface, err := water.New(config)
	if nil != err {
		log.Fatalf("Tun interface init(), Unable to allocate TUN interface: %+v\n", err)
	}

	link, err := netlink.LinkByName(tunName)
	if nil != err {
		log.Fatalf("Tun interface %s Up(), Unable to get interface info %+v\n", tunName, err)
	}
	err = netlink.LinkSetMTU(link, mtu)
	if nil != err {
		log.Fatalf("Tun interface %s Up() Unable to set MTU to %d on interface\n", tunName, mtu)

	}
	err = netlink.LinkSetTxQLen(link, txQLen)
	if nil != err {
		log.Fatalf("Tun interface %s Up() Unable to set MTU to %d on interface\n", tunName, mtu)
	}
	err = netlink.AddrAdd(link, &netlink.Addr{
		IPNet: tunNetwork,
		Label: "",
	})
	if nil != err {
		log.Fatalf("Tun interface %s Up() Unable to set IP to %s / %s on interface: %+v\n", tunName, tunNetwork.IP.String(), tunNetwork.String(), err)
	}

	err = netlink.LinkSetUp(link)
	if nil != err {
		log.Fatalf("Tun interface Up() Unable to UP interface\n")
	}
	log.Printf("Tun interface %s Up() Tun(%s) interface with %s\n", tunName, tunNetwork.IP.String(), tunNetwork.String())

	return tunInterface
}

func printPacketIPs(packet []byte) string {
	return fmt.Sprintf("PROTO: %d, %d.%d.%d.%d=>%d.%d.%d.%d", packet[9],
		packet[srcIPOffset+0], packet[srcIPOffset+1], packet[srcIPOffset+2], packet[srcIPOffset+3],
		packet[dstIPOffset+0], packet[dstIPOffset+1], packet[dstIPOffset+2], packet[dstIPOffset+3])
}

func recalculateIPChecksum(packet []byte) {
	binary.BigEndian.PutUint16(packet[ipHeaderChecksumOffset:ipHeaderChecksumOffset+2], checksum(packet[:ipHeaderLength]))
}

func ipAddressesChecksum(data []byte) uint32 {
	var csum uint32
	csum += (uint32(data[0]) + uint32(data[2])) << 8
	csum += uint32(data[1]) + uint32(data[3])
	csum += (uint32(data[4]) + uint32(data[6])) << 8
	csum += uint32(data[5]) + uint32(data[7])
	return csum
}

func tcpipChecksum(data []byte, csum uint32) uint16 {
	// to handle odd lengths, we loop to length - 1, incrementing by 2, then
	// handle the last byte specifically by checking against the original
	// length.
	length := len(data) - 1
	for i := 0; i < length; i += 2 {
		// For our test packet, doing this manually is about 25% faster
		// (740 ns vs. 1000ns) than doing it by calling binary.BigEndian.Uint16.
		csum += uint32(data[i]) << 8
		csum += uint32(data[i+1])
	}
	if len(data)%2 == 1 {
		csum += uint32(data[length]) << 8
	}
	for csum > 0xffff {
		csum = (csum >> 16) + (csum & 0xffff)
	}
	return ^uint16(csum)
}

func tcpChecksum(packet []byte) uint16 {
	csum := ipAddressesChecksum(packet[srcIPOffset : srcIPOffset+8])
	csum += uint32(packet[protocolOffset])
	length := uint32(len(packet[ipHeaderLength:]))
	csum += length & 0xffff
	csum += length >> 16

	return tcpipChecksum(packet[ipHeaderLength:], csum)
}

func recalculateTCPIPChecksum(packet []byte) {
	//tcpCsum := binary.BigEndian.Uint16(packet[ipHeaderLength+tcpChecksumOffset : ipHeaderLength+tcpChecksumOffset+2])
	binary.BigEndian.PutUint16(packet[ipHeaderLength+tcpChecksumOffset:ipHeaderLength+tcpChecksumOffset+2], 0)
	recalcCsum := tcpChecksum(packet)
	//log.Printf("TCP checksum: origin=%d, recalculated=%d, diff=%d", tcpCsum, recalcCsum, tcpCsum - recalcCsum)
	binary.BigEndian.PutUint16(packet[ipHeaderLength+tcpChecksumOffset:ipHeaderLength+tcpChecksumOffset+2], recalcCsum)
}

func recalculateUDPChecksum(packet []byte) {
	//udpCsum := binary.BigEndian.Uint16(packet[ipHeaderLength+udpChecksumOffset : ipHeaderLength+udpChecksumOffset+2])
	binary.BigEndian.PutUint16(packet[ipHeaderLength+udpChecksumOffset:ipHeaderLength+udpChecksumOffset+2], 0)
	recalcCsum := tcpChecksum(packet)
	//log.Printf("UDP checksum: origin=%d, recalculated=%d, diff=%d", udpCsum, recalcCsum, udpCsum-recalcCsum)
	binary.BigEndian.PutUint16(packet[ipHeaderLength+udpChecksumOffset:ipHeaderLength+udpChecksumOffset+2], recalcCsum)
}

func runTunReadThread(tunInterface *water.Interface, ch chan<- *DataPacket) {
	go func() {
		var packet = make([]byte, mtu)
		for {
			plen, err := tunInterface.Read(packet)
			if err != nil {
				log.Fatalf("Tun Interface Read: type unknown %+v\n", err)
			}

			//log.Printf("IP packet form TUN '%s' %+v", tunInterface.Name(), printPacketIPs(packet))
			if packet[dstIPOffset+2] == 2 && packet[dstIPOffset+3] == 30 {
				//log.Printf("request matched\n")
				packet[srcIPOffset+2] = 1
				packet[srcIPOffset+3] = 30
				packet[dstIPOffset+2] = 1
				packet[dstIPOffset+3] = 10
				recalculateIPChecksum(packet)
				if packet[protocolOffset] == 6 {
					recalculateTCPIPChecksum(packet[:plen])
				}
				if packet[protocolOffset] == 17 {
					recalculateUDPChecksum(packet[:plen])
				}
			}

			if packet[dstIPOffset+2] == 1 && packet[dstIPOffset+3] == 30 {
				//log.Printf("response matched\n")
				packet[srcIPOffset+2] = 2
				packet[srcIPOffset+3] = 30
				packet[dstIPOffset+2] = 2
				packet[dstIPOffset+3] = 10
				recalculateIPChecksum(packet)
				if packet[protocolOffset] == 6 {
					recalculateTCPIPChecksum(packet[:plen])
				}
				if packet[protocolOffset] == 17 {
					recalculateUDPChecksum(packet[:plen])
				}
			}

			//log.Printf("IP packet form TUN '%s' after processing %+v", tunInterface.Name(), printPacketIPs(packet))

			dataPacket := getDataPacket()
			copy(dataPacket.data, packet[:plen])
			dataPacket.packetLen = plen
			ch <- dataPacket
			//log.Printf("TUN packet received\n")
		}
	}()
}

func runTunWriteThread(tunInterface *water.Interface, ch <-chan *DataPacket) {
	go func() {
		for pkt := range ch {
			_, err := tunInterface.Write(pkt.data[:pkt.packetLen])
			putDataPacket(pkt)
			if err != nil {
				log.Fatalf("Tun Interface Write: type unknown %+v\n", err)
			}
			//log.Printf("TUN packet sent\n")
		}
	}()
}

func main() {
	tun1 := createTun(tunIP1, tunName1)
	tun2 := createTun(tunIP2, tunName2)

	var ch1 = make(chan *DataPacket, 1000)
	var ch2 = make(chan *DataPacket, 1000)

	runTunReadThread(tun1, ch1)
	runTunReadThread(tun2, ch2)

	runTunWriteThread(tun1, ch2)
	runTunWriteThread(tun2, ch1)

	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
}
