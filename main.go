package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"syscall"
	"time"

	"golang.org/x/net/ipv4"
)

var debugF = flag.Bool("d", false, "Enable socket level debugging (if supported)")
var ttlF = flag.Int("f", 1, "Specify with what TTL to start. Defaults to 1")
var hopsF = flag.Int("m", 30, "Specify the maximum number of hops (max time-to-live value) the program will probe. The default is 30")
var portF = flag.Int("p", 34500, "Specify the destination port to use. This number will be incremented by each probe")
var probesF = flag.Int("q", 3, "Sets the number of probe packets per hop. The default number is 3")

const (
	dataBytesLen = 16   // amount of data sent on the UDP packet
	readBufSize  = 1024 // buffer size when reading data from the ICMP packet
	probeTimeout = 5    // amount of seconds to wait before the response for a probe times out
)

func main() {
	log.SetPrefix("rt: ")
	log.SetFlags(0)
	flag.Parse()
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage of rt: [-d -f -m -p -q] host\n")
		flag.PrintDefaults()
		os.Exit(1)
	}
	if len(flag.Args()) == 0 {
		log.Printf("A host is required\n")
		flag.Usage()
	}
	if len(flag.Args()) > 1 {
		log.Printf("only 1 destination must be specified\n")
		flag.Usage()
	}
	// TODO: validate the port number, it should be greater than 30,000
	// TODO: make changes to use the process id for the initial port number
	//       in case there's more than 1 traceroute program running

	destination := flag.Args()[0]
	addrs, err := net.LookupHost(destination)
	if err != nil {
		log.Fatalf("lookup for %s failed: %s", destination, err)
	}
	if len(addrs) == 0 {
		log.Fatalf("no addresses were found for %s", destination)
	}
	destinationIP, err := getIPAddr(addrs)
	if err != nil {
		log.Fatalf("IP address not found: %s", err)
	}
	printStart(destination, destinationIP)
	go listenICMP()
	startTrace(destinationIP)
}

type tracePacket struct {
	seqNum int32
	ttl    int32
	ts     int64
}

type probeInfo struct {
	routerIP   net.IP
	routerName string
	icmpType   int
	icmpCode   int
}

var probChan chan *probeInfo

func listenICMP() {
	laddr := net.IPAddr{
		IP: nil,
	}
	conn, err := net.ListenIP("ip4:1", &laddr)
	if err != nil {
		log.Fatalf("error listening for ICPMP packets: %s", err)
	}
	probChan = make(chan *probeInfo)
	for {
		buf := make([]byte, readBufSize)
		_, err = conn.Read(buf)
		if err != nil {
			log.Printf("error reading data: %s", err)
			continue
		}
		pInfo := newProbeInfo(buf)
		probChan <- pInfo
	}
}

func startTrace(destIP net.IP) {
	port := *portF
	var seqNum int
	var done bool
	for ttl := *ttlF; ttl <= *hopsF; ttl++ {
		if done {
			break
		}
		fmt.Printf("%d ", ttl)
		for pro := 0; pro < *probesF; pro++ {
			udpConn, err := connectUDP(destIP, port, ttl)
			if err != nil {
				log.Printf("error connecting: %s", err)
				continue
			}
			if *debugF {
				setSocketDebugOption(udpConn) // ignoring any errors
			}
			seqNum++
			port++
			d := tracePacket{
				seqNum: int32(seqNum),
				ttl:    int32(ttl),
				ts:     time.Now().UnixNano(),
			}
			startTS := d.ts
			_, err = udpConn.Write(getTracePacketData(&d))
			if err != nil {
				log.Printf("error sending data: %s", err)
				continue
			}
			timer := time.NewTimer(probeTimeout * time.Second)
			var pInfo *probeInfo
			select {
			case pInfo = <-probChan:
				timer.Stop()
			case <-timer.C:
				fmt.Printf("* ")
				continue // continue to the next probe
			}
			endTS := time.Now().UnixNano()
			if pro == 0 {
				printRouterIP(pInfo)
			}
			fmt.Printf("%.3f ms   ", float64(endTS-startTS)/1000000.00)
			if isPortUnreachable(pInfo) {
				done = true
			}
		}
		fmt.Println()
	}
}

func connectUDP(destIP net.IP, port int, ttl int) (*net.UDPConn, error) {
	raddr := net.UDPAddr{
		IP:   destIP,
		Port: port,
	}
	udpConn, err := net.DialUDP("udp4", nil, &raddr)
	if err != nil {
		return nil, err
	}
	nconn := ipv4.NewConn(udpConn)
	err = nconn.SetTTL(ttl)
	if err != nil {
		return nil, err
	}
	return udpConn, nil
}

func newProbeInfo(buf []byte) *probeInfo {
	var routerName string
	routerIP := net.IPv4(buf[12], buf[13], buf[14], buf[15])
	icmpType := int(buf[20])
	icmpCode := int(buf[21])

	names, _ := net.LookupAddr(routerIP.String())
	if len(names) > 0 {
		routerName = names[0]
	}
	return &probeInfo{
		routerIP:   routerIP,
		routerName: routerName,
		icmpType:   icmpType,
		icmpCode:   icmpCode,
	}
}

func printRouterIP(pInfo *probeInfo) {
	routerAddr := pInfo.routerIP.String()
	if pInfo.routerName != "" {
		fmt.Printf("%s", pInfo.routerName)
	} else {
		fmt.Printf("%s", routerAddr)
	}
	fmt.Printf(" (%s)", routerAddr)
	fmt.Printf("  ")
}

func isPortUnreachable(pInfo *probeInfo) bool {
	if pInfo.icmpType == 3 && pInfo.icmpCode == 3 {
		return true
	}
	return false
}

func getTracePacketData(data *tracePacket) []byte {
	d := make([]byte, dataBytesLen)
	// TODO: send some data
	// this should have the sequence number
	// the current TTL of the probe
	// and the current timestamp
	d[0] = 1
	d[1] = 2
	d[2] = 3
	d[3] = 4
	d[4] = 5
	return d
}

func printStart(destination string, destinationIP net.IP) {
	fmt.Printf("traceroute to %s", destination)
	if destinationIP != nil {
		fmt.Printf(" (%s),", destinationIP.String())
	}
	fmt.Printf(" %d hops max, %d byte packets\n", *hopsF, dataBytesLen)
}

func getIPAddr(addrs []string) (net.IP, error) {
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip != nil && ip.To4() != nil {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("address not found")
}

func setSocketDebugOption(conn *net.UDPConn) error {
	rc, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	return rc.Control(func(fd uintptr) {
		syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_DEBUG, 1)
	})
}
