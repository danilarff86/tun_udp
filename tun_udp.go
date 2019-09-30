package main

import (
	"github.com/matishsiao/go_reuseport"
	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
	lfqueue "github.com/xiaonanln/go-lockfree-queue"
	"log"
	"net"
	"os"
	"runtime"
	"sync"
)

const (
	tunName string = "tun0"
	txQLen  int    = 5000
	mtu     int    = 1500
)

var serverTunIP net.IP = []byte{10, 0, 1, 254}
var serverUDPIP string = "192.168.1.95"
var serverUDPPort string = "5110"

var clientTunIP net.IP = []byte{10, 0, 1, 1}
var clientUDPIP string = "192.168.1.90"
var clientUDPPort string = "5120"

type DataPacket struct {
	data      []byte
	packetLen int
}

var packetPool *sync.Pool

var tunInterface *water.Interface
var udpListenConn net.PacketConn
var udpWriterConn net.PacketConn
var tunLink netlink.Link
var tunReadChan *lfqueue.Queue = lfqueue.NewQueue(5000)
var udpReadChan *lfqueue.Queue = lfqueue.NewQueue(5000)

func initPool() {
	packetPool = &sync.Pool{
		New: func() interface{} {
			return &DataPacket{data: make([]byte, mtu), packetLen: 0}
		},
	}
}

func init() {
	initPool()
}

func getDataPacket() *DataPacket {
	return packetPool.Get().(*DataPacket)
}

func putDataPacket(p *DataPacket) {
	packetPool.Put(p)
}

func runTunReadThread() {
	go func() {
		var packet = make([]byte, mtu)
		for {
			plen, err := tunInterface.Read(packet)
			if err != nil {
				log.Fatal("Tun Interface Read: type unknown %+v\n", err)
			}
			dataPacket := getDataPacket()
			copy(dataPacket.data, packet[:plen])
			dataPacket.packetLen = plen
			ok := tunReadChan.Put(dataPacket)
			if !ok {
				log.Printf("TUN read queue is FULL")
			}
			//log.Printf("TUN packet received\n")
		}
	}()
}

func runTunWriteThread() {
	go func() {
		for {
			elem, ok := udpReadChan.Get()
			if ok {
				pkt := elem.(*DataPacket)
				_, err := tunInterface.Write(pkt.data[:pkt.packetLen])
				putDataPacket(pkt)
				if err != nil {
					log.Fatal("Tun Interface Write: type unknown %+v\n", err)
				}
				//log.Printf("TUN packet sent\n")
			} else {
				runtime.Gosched()
			}
		}
	}()
}

func createTun(ip net.IP) {
	var tunNetwork *net.IPNet = &net.IPNet{IP: ip, Mask: []byte{255, 255, 255, 0}}

	var config = water.Config{
		DeviceType: water.TUN,
	}

	var err error
	tunInterface, err = water.New(config)
	if nil != err {
		log.Fatal("Tun interface init(), Unable to allocate TUN interface: %+v\n", err)
	}

	link, err := netlink.LinkByName(tunName)
	if nil != err {
		log.Fatal("Tun interface %s Up(), Unable to get interface info %+v\n", tunName, err)
	}
	err = netlink.LinkSetMTU(link, mtu)
	if nil != err {
		log.Fatal("Tun interface %s Up() Unable to set MTU to %d on interface\n", tunName, mtu)

	}
	err = netlink.LinkSetTxQLen(link, txQLen)
	if nil != err {
		log.Fatal("Tun interface %s Up() Unable to set MTU to %d on interface\n", tunName, mtu)
	}
	err = netlink.AddrAdd(link, &netlink.Addr{
		IPNet: tunNetwork,
		Label: "",
	})
	if nil != err {
		log.Fatal("Tun interface %s Up() Unable to set IP to %s / %s on interface: %+v\n", tunName, tunNetwork.IP.String(), tunNetwork.String(), err)
	}

	err = netlink.LinkSetUp(link)
	if nil != err {
		log.Fatal("Tun interface Up() Unable to UP interface\n")
	}
	tunLink = link
	log.Printf("Tun interface %s Up() Tun(%s) interface with %s\n", tunName, tunNetwork.IP.String(), tunNetwork.String())
}

func runUDPReadThread() {
	go func() {
		packet := make([]byte, mtu)
		for {
			plen, _, err := udpListenConn.ReadFrom(packet)
			if err != nil {
				log.Fatal("UDP Interface Read: type unknown %+v\n", err)
			}
			dataPacket := getDataPacket()
			copy(dataPacket.data, packet[:plen])
			dataPacket.packetLen = plen
			ok := udpReadChan.Put(dataPacket)
			if !ok {
				log.Printf("TUN read queue is FULL")
			}
			//log.Printf("UDP packet received\n")
		}
	}()
}

func runUDPWriteThread(addrStr string) {
	addr, err := net.ResolveUDPAddr("", addrStr)
	if err != nil {
		log.Fatal("Unable to resolve UDP address %s: %+v\n", addrStr, err)
	}

	go func() {
		for {
			elem, ok := tunReadChan.Get()
			if ok {
				pkt := elem.(*DataPacket)
				_, err := udpWriterConn.WriteTo(pkt.data[:pkt.packetLen], addr)
				putDataPacket(pkt)
				if err != nil {
					log.Fatal("UDP Interface Write: type unknown %+v\n", err)
				}
				//log.Printf("UDP packet sent\n")
			} else {
				runtime.Gosched()
			}
		}
	}()
}

func createUDPListener(addrStr string) {
	conn, err := reuseport.NewReusableUDPPortConn("udp", addrStr)
	if err != nil {
		log.Fatal("Unable to open UDP listening socket for addr %s: %+v\n", addrStr, err)
	}
	log.Printf("Listening UDP: %s\n", addrStr)
	udpListenConn = conn
}

func createUDPWriter(addrStr string) {
	conn, err := reuseport.NewReusableUDPPortConn("udp", addrStr)
	if err != nil {
		log.Fatal("Unable to open UDP writing socket for addr %s: %+v\n", addrStr, err)
	}
	log.Printf("UDP writing conn: %s\n", addrStr)
	udpWriterConn = conn
}

func usageString() {
	log.Fatal("Usage: %s server|client\n", os.Args[0])
}

func main() {
	argc := len(os.Args)
	if argc < 2 {
		usageString()
	}

	switch os.Args[1] {
	case "server":
		createTun(serverTunIP)
		createUDPListener(serverUDPIP + ":" + serverUDPPort)
		createUDPWriter(serverUDPIP + ":" + clientUDPPort)
		runTunReadThread()
		runUDPReadThread()
		runUDPWriteThread(clientUDPIP + ":" + clientUDPPort)
		runTunWriteThread()
	case "client":
		createTun(clientTunIP)
		createUDPListener(clientUDPIP + ":" + clientUDPPort)
		createUDPWriter(clientUDPIP + ":" + serverUDPPort)
		runTunReadThread()
		runUDPReadThread()
		runUDPWriteThread(serverUDPIP + ":" + serverUDPPort)
		runTunWriteThread()
	default:
		usageString()
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
}
