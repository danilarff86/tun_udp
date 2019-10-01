package main

import (
	"github.com/matishsiao/go_reuseport"
	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
	"golang.org/x/net/ipv4"
	"log"
	"net"
	"os"
	"runtime"
	"sync"
)

const (
	RCVR_BUF_SIZE int = 1500 // Possible to receive jumbo UDP packets
	RCVR_MSG_PACK int = 300  //Max number of messages in one ReadBatch call
)

const (
	tunName string = "tun0"
	txQLen  int    = 5000
	mtu     int    = 1500
)

var serverTunIP net.IP = []byte{10, 0, 1, 254}
var serverUDPIP = "192.168.1.95"
var serverUDPPort = "5110"

var clientTunIP net.IP = []byte{10, 0, 1, 1}
var clientUDPIP = "192.168.1.90"
var clientUDPPort = "5120"

var dstIP *net.UDPAddr

var packetPool *sync.Pool
var messagesPool *sync.Pool

var tunInterface *water.Interface
var udpListenConn *ipv4.PacketConn
var udpWriterConn *ipv4.PacketConn
var tunReadChan = make(chan *DataPacket, 1000)
var udpReadChan = make(chan *DataPacket, 1000)

func initPool() {
	packetPool = &sync.Pool{
		New: func() interface{} {
			return &DataPacket{data: make([]byte, mtu), packetLen: 0}
		},
	}

	messagesPool = &sync.Pool{
		New: func() interface{} {
			return &Batch{getMessageBuffer(RCVR_MSG_PACK), RCVR_MSG_PACK}
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
			tunReadChan <- dataPacket
			//log.Printf("TUN packet received\n")
		}
	}()
}

func runTunWriteThread() {
	go func() {
		runtime.LockOSThread()
		for pkt := range udpReadChan {
			_, err := tunInterface.Write(pkt.data[:pkt.packetLen])
			putDataPacket(pkt)
			if err != nil {
				log.Fatalf("Tun Interface Write: type unknown %+v\n", err)
			}
			//log.Printf("TUN packet sent\n")
		}
	}()
}

func createTun(ip net.IP) {
	var tunNetwork = &net.IPNet{IP: ip, Mask: []byte{255, 255, 255, 0}}

	var config = water.Config{
		DeviceType: water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name: tunName,
		},
	}

	var err error
	tunInterface, err = water.New(config)
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
}

func runUDPReadThread() {
	go func() {
		runtime.LockOSThread()
		packet := make([]byte, mtu)
		for {
			plen, _, _, err := udpListenConn.ReadFrom(packet)
			if err != nil {
				log.Fatalf("UDP Interface Read: type unknown %+v\n", err)
			}
			dataPacket := getDataPacket()
			copy(dataPacket.data, packet[:plen])
			dataPacket.packetLen = plen
			udpReadChan <- dataPacket
			//log.Printf("UDP packet received\n")
		}
	}()
}

func runUDPWriteThread(addrStr string) {
	addr, err := net.ResolveUDPAddr("", addrStr)
	if err != nil {
		log.Fatalf("Unable to resolve UDP address %s: %+v\n", addrStr, err)
	}

	go func() {
		runtime.LockOSThread()
		for pkt := range tunReadChan {
			_, err := udpWriterConn.WriteTo(pkt.data[:pkt.packetLen], nil, addr)
			putDataPacket(pkt)
			if err != nil {
				log.Fatalf("UDP Interface Write: type unknown %+v\n", err)
			}
			//log.Printf("UDP packet sent\n")
		}
	}()
}

func createUDPListener(addrStr string) {
	laddr, errl := net.ResolveUDPAddr("udp4", addrStr)
	if errl != nil {
		log.Fatalf("Unable to open UDP listening socket for addr %s: %+v\n", addrStr, errl)
	}

	ln, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		log.Fatalf("Unable to open UDP listening socket for addr %s: %+v\n", addrStr, err)
	}
	udpListenConn = ipv4.NewPacketConn(ln)
}

func createUDPWriter(addrStr string) {
	conn, err := reuseport.NewReusableUDPPortConn("udp", addrStr)
	if err != nil {
		log.Fatalf("Unable to open UDP writing socket for addr %s: %+v\n", addrStr, err)
	}
	log.Printf("UDP writing conn: %s\n", addrStr)
	udpWriterConn = ipv4.NewPacketConn(conn)
}

func usageString() {
	log.Fatalf("Usage: %s server|client\n", os.Args[0])
}

func main() {
	argc := len(os.Args)
	if argc < 2 {
		usageString()
	}
	go RunMonitor()
	switch os.Args[1] {
	case "server":
		dstIP, _ = net.ResolveUDPAddr("udp", clientUDPIP+":"+clientUDPPort)
		createTun(serverTunIP)
		createUDPListener(serverUDPIP + ":" + serverUDPPort)
		createUDPWriter(serverUDPIP + ":" + clientUDPPort)
		//runTunReadThread()
		//runUDPReadThread()
		//runUDPWriteThread(clientUDPIP + ":" + clientUDPPort)
		//runTunWriteThread()

		tunUdp := RunNewPacketCollector(RCVR_MSG_PACK)
		runTunReadBatchThread(tunUdp)
		runUDPBatchWriteThread(tunUdp)
		udpTun := RunNewPacketCollector(RCVR_MSG_PACK)
		runUDPReadBatchThread(udpTun)
		runTunWriteBatchThread(udpTun)
	case "client":
		dstIP, _ = net.ResolveUDPAddr("udp", serverUDPIP+":"+serverUDPPort)
		createTun(clientTunIP)
		createUDPListener(clientUDPIP + ":" + clientUDPPort)
		createUDPWriter(clientUDPIP + ":" + serverUDPPort)
		//runTunReadThread()
		//runUDPReadThread()
		//runUDPWriteThread(serverUDPIP + ":" + serverUDPPort)
		//runTunWriteThread()

		tunUdp := RunNewPacketCollector(RCVR_MSG_PACK)
		runTunReadBatchThread(tunUdp)
		runUDPBatchWriteThread(tunUdp)
		udpTun := RunNewPacketCollector(RCVR_MSG_PACK)
		runUDPReadBatchThread(udpTun)
		runTunWriteBatchThread(udpTun)
	default:
		usageString()
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
}
