/*
 * Copyright (C) 2022 Ain Ghazal. All Rights Reversed.
 */

package extras

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"

	"github.com/ooni/minivpn/vpn"
)

const (
	// time, in seconds, before we timeout the connection used for sending an ECHO request.
	timeoutSeconds = 10
)

// NewPinger returns a pointer to a Pinger struct configured to handle data from a
// vpn.Client. It needs host and count as parameters, and also accepts a done
// channel in which termination of the measurement series will be notified.
func NewPinger(rawDialer *vpn.RawDialer, host string, count int) *Pinger {
	// TODO validate host ip / domain
	id := os.Getpid() & 0xffff
	ts := make(map[int]int64)
	return &Pinger{
		raw:      rawDialer,
		host:     host,
		ts:       ts,
		Count:    int(count),
		Interval: 1,
		ID:       id,
		ttl:      64,
	}
}

// st holds some stats about a single icmp
type st struct {
	rtt float32
	ttl uint8
}

func (s st) RTT() float32 {
	return s.rtt
}

func (s st) TTL() uint8 {
	return s.ttl
}

// Pinger holds all the needed info to ping a target.
type Pinger struct {
	raw *vpn.RawDialer
	st  []st
	// stats mutex
	// mu sync.Mutex
	// send payload mutex
	// pmu sync.Mutex

	host string

	Count    int
	Interval time.Duration
	ID       int

	ts map[int]int64

	packetsSent int
	packetsRecv int
	ttl         int
}

func (p *Pinger) printStats() {
	if p.packetsSent == 0 {
		return
	}
	fmt.Println("--- " + p.host + " ping statistics ---")
	loss := (p.packetsRecv / p.packetsSent) / 100
	var r []float32
	var sum, sd, min, max float32
	min = p.st[0].rtt
	for _, s := range p.st {
		r = append(r, s.rtt)
		sum += s.rtt
		if s.rtt < min {
			min = s.rtt
		}
		if s.rtt > max {
			max = s.rtt
		}
	}
	avg := float32(float32(sum) / float32(len(r)))
	for _, s := range p.st {
		sd += float32(math.Pow(float64(s.rtt-avg), 2))
	}
	sd = float32(math.Sqrt(float64(sd / float32(len(r)))))
	fmt.Printf("%d packets transmitted, %d received, %d%% packet loss\n", p.packetsSent, p.packetsRecv, loss)
	fmt.Printf("rtt min/avg/max/stdev = %.3f, %.3f, %.3f, %.3f ms\n", min, avg, max, sd)
}

func (p *Pinger) Run() error {
	conn, err := p.raw.Dial()
	if err != nil {
		log.Println("Error while dialing a VPN connection:", err.Error())
		return err
	}

	for i := 0; i < p.Count; i++ {
		// TODO go back to different send/receive routines, here the send/receive delays are interfering.
		src := conn.LocalAddr().String()
		srcIP := net.ParseIP(src)
		dstIP := net.ParseIP(p.host)
		start := time.Now()
		ipck := newIcmpData(&srcIP, &dstIP, 8, p.ttl, i, p.ID)
		_, err = conn.Write(ipck)
		if err != nil {
			return err
		}
		p.packetsSent++

		// TODO add timeout to this read
		buf := make([]byte, 1500)
		_, err = conn.Read(buf)
		if err != nil {
			return err
		}
		p.packetsRecv++

		// TODO this is the naive way of doing timestamps, equivalent to "ping -U",
		// but I expect it to be unstable under certain circumstances (high CPU load, GC pauses etc).
		// It'd be a better idea to try to use kernel capabilities if available (need to research what's possible in osx/windows, possibly have a fallback to the naive way).
		// in case we do see that load produces instability.
		// https://coroot.com/blog/how-to-ping
		end := time.Now()
		p.parseEchoReply(buf, conn.LocalAddr().String(), start, end)
		time.Sleep(1 * time.Second)
	}
	return nil
}

// Stop prints ping statistics before quitting.
func (p *Pinger) Stop() {
	p.printStats()
}

// Starts return the measured stats.
func (p *Pinger) Stats() []st {
	return p.st
}

func newIcmpData(src, dest *net.IP, typeCode, ttl, seq, id int) (data []byte) {
	ip := &layers.IPv4{}
	ip.Version = 4
	ip.Protocol = layers.IPProtocolICMPv4
	ip.SrcIP = *src
	ip.DstIP = *dest

	ip.Length = 20
	ip.TTL = uint8(ttl)

	icmp := &layers.ICMPv4{}
	icmp.TypeCode = layers.ICMPv4TypeCode(uint16(typeCode) << 8)
	icmp.Id = uint16(id)
	icmp.Seq = uint16(seq)
	icmp.Checksum = 0

	opts := gopacket.SerializeOptions{}
	opts.ComputeChecksums = true
	opts.FixLengths = true

	now := time.Now().UnixNano()
	var payload = make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, uint64(now))

	buf := gopacket.NewSerializeBuffer()
	err := gopacket.SerializeLayers(buf, opts, ip, icmp, gopacket.Payload(payload))
	if err != nil {
		log.Println("error:", err)
	}

	return buf.Bytes()
}

func (p *Pinger) parseEchoReply(d []byte, dst string, start, end time.Time) {
	ip := layers.IPv4{}
	udp := layers.UDP{}
	icmp := layers.ICMPv4{}
	payload := gopacket.Payload{}
	decoded := []gopacket.LayerType{}
	parser := gopacket.NewDecodingLayerParser(layers.LayerTypeIPv4, &ip, &icmp, &udp, &payload)

	err := parser.DecodeLayers(d, &decoded)
	if err != nil {
		log.Println("error decoding:", err)
		return
	}

	for _, layerType := range decoded {
		switch layerType {
		case layers.LayerTypeIPv4:
			if ip.DstIP.String() != dst {
				log.Println("warn: icmp response with wrong dst")
				return
			}
			if ip.SrcIP.String() != p.host {
				log.Println("warn: icmp response with wrong src")
				return
			}
		case layers.LayerTypeICMPv4:
			if icmp.Id != uint16(p.ID) {
				log.Println("warn: icmp response with wrong ID")
				return
			}
		}
	}
	du := end.Sub(start)
	rtt := float32(du/time.Microsecond) / 1000
	fmt.Printf("reply from %s: icmp_seq=%d ttl=%d time=%.1f ms\n", ip.SrcIP, icmp.Seq, ip.TTL, rtt)
	p.st = append(p.st, st{rtt, ip.TTL})
}
