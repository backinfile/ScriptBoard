package redismanager

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIPv6Address(t *testing.T) {
	for _, mode := range []TLSMode{TLSDisabled, TLSVerifyIdentity, TLSInsecureSkipVerify} {
		t.Run(string(mode), func(t *testing.T) {
			m, err := New(Options{DB: openTestDatabase(t), StateRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			i, err := m.SaveInstance(context.Background(), InstanceInput{Name: "ipv6", Host: "::1", Port: 6379, TLSMode: mode})
			if err != nil {
				t.Fatal(err)
			}
			c, err := m.ExecutionBackend().(*localBackend).client(i, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			host, port, err := net.SplitHostPort(c.Options().Addr)
			if err != nil || host != "::1" || port != "6379" {
				t.Fatalf("IPv6 address=%q: %v", c.Options().Addr, err)
			}
			if (c.Options().TLSConfig != nil) != (mode != TLSDisabled) {
				t.Fatal("transport mode changed")
			}
			if mode != TLSDisabled && (c.Options().TLSConfig.InsecureSkipVerify != (mode == TLSInsecureSkipVerify) || c.Options().TLSConfig.ServerName != "::1") {
				t.Fatal("TLS identity choice changed")
			}
		})
	}
}
func TestExplicitPasswordlessConnectionCanChangePort(t *testing.T) {
	for _, mode := range []TLSMode{TLSDisabled, TLSVerifyIdentity, TLSInsecureSkipVerify} {
		t.Run(string(mode), func(t *testing.T) {
			m, err := New(Options{DB: openTestDatabase(t), StateRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			input := InstanceInput{Name: "cache", Host: "127.0.0.1", Port: 6379, TLSMode: TLSDisabled, Password: "previous-secret"}
			i, err := m.SaveInstance(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			input.ID = i.ID
			input.Password = ""
			input.ClearPassword = true
			input.Port = 6380
			input.TLSMode = mode
			i, err = m.SaveInstance(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			password, err := m.ExecutionBackend().(*localBackend).credentials.get(i)
			if err != nil || password != "" {
				t.Fatalf("explicit empty credential not stored: %v", err)
			}
			input.ClearPassword = false
			input.Name = "renamed"
			if _, err := m.SaveInstance(ctx, input); err != nil {
				t.Fatal(err)
			}
			input.Port = 6381
			if _, err := m.SaveInstance(ctx, input); err == nil {
				t.Fatal("endpoint rebound without explicit credentials")
			}
			input.ClearPassword = true
			if _, err := m.SaveInstance(ctx, input); err != nil {
				t.Fatalf("passwordless endpoint change: %v", err)
			}
			input.Password = "conflicting-secret"
			if _, err := m.SaveInstance(ctx, input); err == nil {
				t.Fatal("conflicting credential choices accepted")
			}
		})
	}
}

func TestRedisConnectsWithEachTransport(t *testing.T) {
	certificateServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := certificateServer.TLS.Certificates[0]
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateServer.Certificate().Raw})
	certificateServer.Close()
	for _, host := range []string{"127.0.0.1", "::1"} {
		for _, mode := range []TLSMode{TLSDisabled, TLSVerifyIdentity, TLSInsecureSkipVerify} {
			t.Run(host+"/"+string(mode), func(t *testing.T) {
				listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
				if err != nil {
					t.Fatal(err)
				}
				if mode != TLSDisabled {
					listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
				}
				defer listener.Close()
				go func() {
					for {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						go serveRedisFixture(conn)
					}
				}()
				_, portText, _ := net.SplitHostPort(listener.Addr().String())
				port, _ := strconv.Atoi(portText)
				root := t.TempDir()
				caPath := ""
				if mode == TLSVerifyIdentity {
					caPath = filepath.Join(root, "ca.pem")
					if err := os.WriteFile(caPath, ca, 0600); err != nil {
						t.Fatal(err)
					}
				}
				m, err := New(Options{DB: openTestDatabase(t), StateRoot: root})
				if err != nil {
					t.Fatal(err)
				}
				i, err := m.SaveInstance(context.Background(), InstanceInput{Name: "transport", Host: host, Port: port, TLSMode: mode, CAPath: caPath})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				result, err := m.TestInstance(ctx, i.ID)
				if err != nil || !result.OK || result.TLS != (mode != TLSDisabled) {
					t.Fatalf("connection failed: %+v %v", result, err)
				}
			})
		}
	}
}
func serveRedisFixture(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
		if err != nil || n < 1 || n > 20 {
			return
		}
		args := make([]string, n)
		for i := range args {
			line, err = reader.ReadString('\n')
			if err != nil {
				return
			}
			size, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "$")))
			if err != nil || size < 0 || size > 4096 {
				return
			}
			body := make([]byte, size+2)
			if _, err = io.ReadFull(reader, body); err != nil {
				return
			}
			args[i] = string(body[:size])
		}
		reply := "-ERR unsupported command\r\n"
		switch strings.ToLower(args[0]) {
		case "hello":
			reply = "-ERR unknown command 'hello'\r\n"
		case "ping":
			reply = "+PONG\r\n"
		case "client", "auth", "select":
			reply = "+OK\r\n"
		}
		if _, err := fmt.Fprint(conn, reply); err != nil {
			return
		}
	}
}
