package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

// Android's bionic resolver uses this socket to query the system DNS daemon.
const androidDNSProxySocket = "/dev/socket/dnsproxyd"

const (
	androidDNSCommandLimit = 512
	androidDNSAnswerLimit  = 64 << 10
	androidDNSPacketLimit  = 1<<16 - 1
)

// maybeInstallAndroidDNSResolver selects Android's per-network resolver for
// static Linux binaries running under Android, where Go has no usable
// /etc/resolv.conf. The property service path distinguishes Android from Linux.
func maybeInstallAndroidDNSResolver() bool {
	// Android commonly has an empty resolv.conf (sometimes via /etc), so its
	// existence cannot distinguish a usable host resolver from Android.
	if _, err := os.Stat("/dev/__properties__"); err != nil {
		return false
	}

	conn, err := net.DialTimeout("unix", androidDNSProxySocket, 5*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()

	*net.DefaultResolver = *newAndroidDNSResolver(androidDNSProxySocket)
	return true
}

func newAndroidDNSResolver(socketPath string) *net.Resolver {
	return newAndroidDNSResolverWithDialer(func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	})
}

func newAndroidDNSResolverWithDialer(dialUpstream func(context.Context) (net.Conn, error)) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			if !strings.HasPrefix(network, "udp") && !strings.HasPrefix(network, "tcp") {
				return nil, fmt.Errorf("unsupported DNS network %q", network)
			}
			upstream, err := dialUpstream(ctx)
			if err != nil {
				return nil, fmt.Errorf("connect to Android DNS proxy: %w", err)
			}

			resolverConn, proxyConn := net.Pipe()
			go serveAndroidDNSQuery(ctx, network, proxyConn, upstream)
			if strings.HasPrefix(network, "udp") {
				return packetConnAdapter{Conn: resolverConn}, nil
			}
			return resolverConn, nil
		},
	}
}

type packetConnAdapter struct {
	net.Conn
}

func (c packetConnAdapter) ReadFrom(buffer []byte) (int, net.Addr, error) {
	n, err := c.Conn.Read(buffer)
	return n, c.Conn.RemoteAddr(), err
}

func (c packetConnAdapter) WriteTo(buffer []byte, _ net.Addr) (int, error) {
	return c.Conn.Write(buffer)
}

func serveAndroidDNSQuery(ctx context.Context, network string, client, upstream net.Conn) {
	defer client.Close()
	defer upstream.Close()
	stopCancellation := context.AfterFunc(ctx, func() {
		_ = client.Close()
		_ = upstream.Close()
	})
	defer stopCancellation()

	query, tcp, err := readResolverDNSQuery(client, network)
	if err != nil {
		return
	}
	answer, err := androidDNSExchange(ctx, upstream, query)
	if err != nil {
		return
	}
	if tcp {
		if len(answer) > androidDNSPacketLimit {
			return
		}
		framedAnswer := make([]byte, 2+len(answer))
		binary.BigEndian.PutUint16(framedAnswer, uint16(len(answer)))
		copy(framedAnswer[2:], answer)
		_, _ = client.Write(framedAnswer)
		return
	}
	_, _ = client.Write(answer)
}

func readResolverDNSQuery(conn net.Conn, network string) ([]byte, bool, error) {
	if strings.HasPrefix(network, "tcp") {
		var sizeBytes [2]byte
		if _, err := io.ReadFull(conn, sizeBytes[:]); err != nil {
			return nil, true, err
		}
		size := int(binary.BigEndian.Uint16(sizeBytes[:]))
		if size == 0 {
			return nil, true, fmt.Errorf("empty DNS query")
		}
		query := make([]byte, size)
		if _, err := io.ReadFull(conn, query); err != nil {
			return nil, true, err
		}
		return query, true, nil
	}
	query := make([]byte, androidDNSPacketLimit)
	n, err := conn.Read(query)
	if err != nil {
		return nil, false, err
	}
	return query[:n], false, nil
}

func androidDNSExchange(ctx context.Context, conn net.Conn, query []byte) ([]byte, error) {
	if len(query) < 12 {
		return nil, fmt.Errorf("Android DNS query is shorter than a DNS header")
	}
	command := "resnsend 0 0 " + base64.StdEncoding.EncodeToString(query) + "\x00"
	if len(command) > androidDNSCommandLimit {
		return nil, fmt.Errorf("Android DNS query exceeds the dnsproxyd command limit")
	}

	stopCancellation := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancellation()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("set Android DNS deadline: %w", err)
		}
	}

	n, err := conn.Write([]byte(command))
	if err != nil {
		return nil, fmt.Errorf("write Android DNS query: %w", err)
	}
	if n != len(command) {
		return nil, io.ErrShortWrite
	}
	if closeWriter, ok := conn.(interface{ CloseWrite() error }); ok {
		if err := closeWriter.CloseWrite(); err != nil {
			return nil, fmt.Errorf("close Android DNS query: %w", err)
		}
	}

	var responseCode [4]byte
	if _, err := io.ReadFull(conn, responseCode[:]); err != nil {
		return nil, fmt.Errorf("read Android DNS result: %w", err)
	}
	rcode := int32(binary.BigEndian.Uint32(responseCode[:]))
	if rcode < 0 {
		return nil, fmt.Errorf("Android DNS proxy: %w", syscall.Errno(-rcode))
	}
	if rcode > 15 {
		return nil, fmt.Errorf("Android DNS proxy returned invalid response code %d", rcode)
	}

	if _, err := io.ReadFull(conn, responseCode[:]); err != nil {
		return nil, fmt.Errorf("read Android DNS answer length: %w", err)
	}
	answerLength := int32(binary.BigEndian.Uint32(responseCode[:]))
	if answerLength < 12 || answerLength > androidDNSAnswerLimit {
		return nil, fmt.Errorf("Android DNS proxy returned invalid answer length %d", answerLength)
	}
	answer := make([]byte, int(answerLength))
	if _, err := io.ReadFull(conn, answer); err != nil {
		return nil, fmt.Errorf("read Android DNS answer: %w", err)
	}
	answer[2] |= 0x80
	answer[3] = answer[3]&0xf0 | byte(rcode)
	return answer, nil
}
