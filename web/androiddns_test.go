package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestAndroidDNSResolverUsesDNSProxy(t *testing.T) {
	for _, network := range []string{"udp", "tcp"} {
		t.Run(network, func(t *testing.T) {
			query := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
			answer := append([]byte(nil), query...)
			answer[2] = 0x81
			answer[3] = 0x80
			serverErr := make(chan error, 1)
			resolver := newAndroidDNSResolverWithDialer(func(context.Context) (net.Conn, error) {
				client, daemon := net.Pipe()
				go func() {
					defer daemon.Close()
					command, err := bufio.NewReader(daemon).ReadBytes(0)
					if err != nil {
						serverErr <- err
						return
					}
					wantCommand := "resnsend 0 0 " + base64.StdEncoding.EncodeToString(query) + "\x00"
					if got := string(command); got != wantCommand {
						serverErr <- io.ErrUnexpectedEOF
						return
					}

					var response []byte
					response = binary.BigEndian.AppendUint32(response, 3) // NXDOMAIN
					response = binary.BigEndian.AppendUint32(response, uint32(len(answer)))
					response = append(response, answer...)
					_, err = daemon.Write(response)
					serverErr <- err
				}()
				return client, nil
			})
			var err error
			conn, err := resolver.Dial(context.Background(), network, "127.0.0.1:53")
			if err != nil {
				t.Fatalf("resolver Dial() error = %v", err)
			}
			defer conn.Close()
			if _, isPacketConn := conn.(net.PacketConn); strings.HasPrefix(network, "udp") && !isPacketConn {
				t.Error("UDP resolver connection does not implement net.PacketConn")
			}

			request := query
			if strings.HasPrefix(network, "tcp") {
				request = make([]byte, 2+len(query))
				binary.BigEndian.PutUint16(request, uint16(len(query)))
				copy(request[2:], query)
			}
			if _, err := conn.Write(request); err != nil {
				t.Fatalf("DNS request Write() error = %v", err)
			}

			response := make([]byte, androidDNSPacketLimit+2)
			n, err := conn.Read(response)
			if err != nil {
				t.Fatalf("DNS response Read() error = %v", err)
			}
			response = response[:n]
			if strings.HasPrefix(network, "tcp") {
				if len(response) < 2 || int(binary.BigEndian.Uint16(response)) != len(response)-2 {
					t.Fatalf("TCP DNS response has invalid length prefix: %x", response)
				}
				response = response[2:]
			}
			if len(response) != len(answer) {
				t.Fatalf("DNS response length = %d, want %d", len(response), len(answer))
			}
			if response[2]&0x80 == 0 {
				t.Error("DNS response does not have the response flag set")
			}
			if got := response[3] & 0x0f; got != 3 {
				t.Errorf("DNS response code = %d, want NXDOMAIN", got)
			}
			if got := binary.BigEndian.Uint16(response); got != binary.BigEndian.Uint16(query) {
				t.Errorf("DNS response ID = %04x, want %04x", got, binary.BigEndian.Uint16(query))
			}
			if err := <-serverErr; err != nil {
				t.Fatalf("DNS proxy test server error = %v", err)
			}
		})
	}
}

func TestAndroidDNSExchangeRejectsOversizedCommand(t *testing.T) {
	query := make([]byte, androidDNSCommandLimit)
	if _, err := androidDNSExchange(context.Background(), nil, query); err == nil {
		t.Fatal("androidDNSExchange() accepted an oversized query")
	}
}

func TestAndroidDNSResolverLookupIP(t *testing.T) {
	serverErr := make(chan error, 1)
	resolver := newAndroidDNSResolverWithDialer(func(context.Context) (net.Conn, error) {
		client, daemon := net.Pipe()
		go func() {
			defer daemon.Close()
			command, err := bufio.NewReader(daemon).ReadBytes(0)
			if err != nil {
				serverErr <- err
				return
			}
			fields := strings.Fields(strings.TrimSuffix(string(command), "\x00"))
			if len(fields) != 4 || fields[0] != "resnsend" {
				serverErr <- io.ErrUnexpectedEOF
				return
			}
			query, err := base64.StdEncoding.DecodeString(fields[3])
			if err != nil || len(query) < 12 {
				serverErr <- io.ErrUnexpectedEOF
				return
			}
			questionEnd := 12
			for {
				if questionEnd >= len(query) {
					serverErr <- io.ErrUnexpectedEOF
					return
				}
				labelLength := int(query[questionEnd])
				questionEnd++
				if labelLength == 0 {
					break
				}
				questionEnd += labelLength
			}
			questionEnd += 4
			if questionEnd > len(query) {
				serverErr <- io.ErrUnexpectedEOF
				return
			}

			answer := append([]byte(nil), query[:questionEnd]...)
			answer[2] |= 0x80
			answer[3] |= 0x80
			binary.BigEndian.PutUint16(answer[6:8], 1)
			binary.BigEndian.PutUint16(answer[10:12], 0)
			answer = append(answer, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 30, 0, 4, 192, 0, 2, 1)

			var response []byte
			response = binary.BigEndian.AppendUint32(response, 0)
			response = binary.BigEndian.AppendUint32(response, uint32(len(answer)))
			response = append(response, answer...)
			_, err = daemon.Write(response)
			serverErr <- err
		}()
		return client, nil
	})

	addresses, err := resolver.LookupNetIP(context.Background(), "ip4", "example.test")
	if err != nil {
		t.Fatalf("LookupNetIP() error = %v", err)
	}
	if len(addresses) != 1 || addresses[0] != netip.MustParseAddr("192.0.2.1") {
		t.Fatalf("LookupNetIP() = %v, want [192.0.2.1]", addresses)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("DNS proxy test server error = %v", err)
	}
}
