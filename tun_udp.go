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
	tunName string = "tun0"
	txQLen  int    = 5000
	mtu     int    = 1500
)

var serverTunIP net.IP = []byte{10, 0, 1, 254}
var serverUDPIP = "192.168.1.120"
var serverUDPPort = "5110"

var clientTunIP net.IP = []byte{10, 0, 1, 1}
var clientUDPIP = "192.168.1.90"
var clientUDPPort = "5120"

var tunInterface *water.Interface
var udpListenConn *ipv4.PacketConn
var udpWriterConn net.PacketConn

func runUDPSenderThread(addrStr string) {
	addr, err := net.ResolveUDPAddr("", addrStr)
	if err != nil {
		log.Fatalf("Unable to resolve UDP address %s: %+v\n", addrStr, err)
	}

	go func() {
		runtime.LockOSThread()
		var packet = make([]byte, mtu)
		for {
			plen, err := tunInterface.Read(packet)
			if err != nil {
				log.Fatalf("Tun Interface Read: type unknown %+v\n", err)
			}
			_, err = udpWriterConn.WriteTo(packet[:plen], addr)
			if err != nil {
				log.Fatalf("UDP Interface Write: type unknown %+v\n", err)
			}
		}
	}()
}

func runUDPReceiverThread() {
	go func() {
		runtime.LockOSThread()
		packet := make([]byte, mtu)
		for {
			plen, _, _, err := udpListenConn.ReadFrom(packet)
			if err != nil {
				log.Fatalf("UDP Interface Read: type unknown %+v\n", err)
			}
			_, err = tunInterface.Write(packet[:plen])
			if err != nil {
				log.Fatalf("Tun Interface Write: type unknown %+v\n", err)
			}
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
	log.Fatalf("Usage: %s server|client\n", os.Args[0])
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
		runUDPReceiverThread()
		runUDPSenderThread(clientUDPIP + ":" + clientUDPPort)
	case "client":
		createTun(clientTunIP)
		createUDPListener(clientUDPIP + ":" + clientUDPPort)
		createUDPWriter(clientUDPIP + ":" + serverUDPPort)
		runUDPReceiverThread()
		runUDPSenderThread(serverUDPIP + ":" + serverUDPPort)
	default:
		usageString()
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
}
