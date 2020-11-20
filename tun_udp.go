package main

import (
	"flag"
	"log"
	"net"
	"os"
	"runtime"
	"sync"

	reuseport "github.com/matishsiao/go_reuseport"
	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
	"golang.org/x/net/ipv4"
)

const (
	tunName        string = "tun0"
	txQLen         int    = 5000
	mtu            int    = 1500
	maxPacketLen   int    = 1460
	isFullPacket   byte   = 0
	isFirstPacket  byte   = 1
	isSecondPacket byte   = 2
)

var serverTunIP net.IP = []byte{10, 0, 1, 254}
var serverUDPPort = "5110"

var clientTunIP net.IP = []byte{10, 0, 1, 1}
var clientUDPPort = "5120"

type DataPacket struct {
	data      []byte
	packetLen int
}

var packetPool *sync.Pool

var tunInterface *water.Interface
var udpListenConn *ipv4.PacketConn
var udpWriterConn net.PacketConn
var tunReadChan = make(chan *DataPacket, 1000)
var udpReadChan = make(chan *DataPacket, 1000)

var firstPacket *DataPacket

func init() {
	packetPool = &sync.Pool{
		New: func() interface{} {
			return &DataPacket{data: make([]byte, mtu+1), packetLen: 0}
		},
	}
}

func getAllowedPacketLen(plen int) int {
	if plen > maxPacketLen {
		return maxPacketLen
	}
	return plen
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

			allowedLen := getAllowedPacketLen(plen)
			dataPacket := getDataPacket()
			copy(dataPacket.data[1:], packet[:allowedLen])
			dataPacket.packetLen = allowedLen + 1

			if plen > maxPacketLen {
				// log.Printf("TUN: big packet received. Start %d, end %d, len %d\n", packet[0], packet[plen-1], plen)
				dataPacket.data[0] = isFirstPacket
				tunReadChan <- dataPacket

				secondPacket := getDataPacket()
				copy(secondPacket.data[1:], packet[maxPacketLen:plen])
				secondPacket.packetLen = plen - maxPacketLen + 1
				secondPacket.data[0] = isSecondPacket
				tunReadChan <- secondPacket
			} else {
				dataPacket.data[0] = isFullPacket
				tunReadChan <- dataPacket
			}
			//log.Printf("TUN packet received\n")
		}
	}()
}

func writeToTun(data []byte) {
	_, err := tunInterface.Write(data)
	if err != nil {
		log.Fatalf("Tun Interface Write: type unknown %+v\n", err)
	}
}

func runTunWriteThread() {
	go func() {
		runtime.LockOSThread()
		for pkt := range udpReadChan {
			switch pkt.data[0] {
			case isFullPacket:
				writeToTun(pkt.data[1:pkt.packetLen])
				putDataPacket(pkt)
			case isFirstPacket:
				if firstPacket != nil {
					putDataPacket(firstPacket)
				}
				firstPacket = pkt
			case isSecondPacket:
				copy(firstPacket.data[firstPacket.packetLen:], pkt.data[1:pkt.packetLen])
				firstPacket.packetLen += pkt.packetLen - 1

				writeToTun(firstPacket.data[1:firstPacket.packetLen])
				// log.Printf("TUN: big packet sent. Start %d, end %d, len %d\n", firstPacket.data[1], firstPacket.data[firstPacket.packetLen-1], firstPacket.packetLen-1)
				putDataPacket(pkt)
				putDataPacket(firstPacket)
				firstPacket = nil
			default:
				putDataPacket(pkt)
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
			_, err := udpWriterConn.WriteTo(pkt.data[:pkt.packetLen], addr)
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
	udpWriterConn = conn
}

func usageString() {
	log.Fatalf("Usage: %s -mode=server|client -server-wan-ip=X.X.X.X -client-wan-ip=X.X.X.X\n", os.Args[0])
}

func main() {
	mode := flag.String("mode", "", "Working mode ('server' or 'client')")
	serverUDPIP := flag.String("server-wan-ip", "", "Server WAN IP")
	clientUDPIP := flag.String("client-wan-ip", "", "Client WAN IP")
	flag.Parse()

	log.Printf("Mode: %s, server-wan-ip: %s:%s, client-wan-ip: %s:%s",
		*mode, *serverUDPIP, serverUDPPort, *clientUDPIP, clientUDPPort)

	switch *mode {
	case "server":
		createTun(serverTunIP)
		createUDPListener(*serverUDPIP + ":" + serverUDPPort)
		createUDPWriter(*serverUDPIP + ":" + clientUDPPort)
		runTunReadThread()
		runUDPReadThread()
		runUDPWriteThread(*clientUDPIP + ":" + clientUDPPort)
		runTunWriteThread()
	case "client":
		createTun(clientTunIP)
		createUDPListener(*clientUDPIP + ":" + clientUDPPort)
		createUDPWriter(*clientUDPIP + ":" + serverUDPPort)
		runTunReadThread()
		runUDPReadThread()
		runUDPWriteThread(*serverUDPIP + ":" + serverUDPPort)
		runTunWriteThread()
	default:
		usageString()
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
}
