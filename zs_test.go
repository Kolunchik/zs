package zs

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockConn struct {
	readData  [][]byte
	readIndex int
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	if m.readIndex >= len(m.readData) {
		return 0, io.EOF
	}
	data := m.readData[m.readIndex]
	n = copy(b, data)
	m.readIndex++
	return n, nil
}

func (m *mockConn) Write(b []byte) (n int, err error)  { return len(b), nil }
func (m *mockConn) Close() error                       { return nil }
func (m *mockConn) LocalAddr() net.Addr                { return nil }
func (m *mockConn) RemoteAddr() net.Addr               { return nil }
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func startTestServer(t *testing.T, handler func(net.Conn)) (net.Listener, int) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		defer listener.Close()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handler(conn)
		}
	}()

	return listener, listener.Addr().(*net.TCPAddr).Port
}

func sendSuccessResponse(conn net.Conn) {
	response := ZabbixResponse{
		Response:  "success",
		Processed: 1,
		Info:      "Processed 1 Failed 0 Total 1",
	}
	jsonData, _ := json.Marshal(response)
	packet, _ := buildPacket(jsonData)
	conn.Write(packet)
}

func TestReadResponse(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		testData := []byte(`{"response":"success","info":"test"}`)
		header := make([]byte, 13)
		copy(header, "ZBXD")
		header[4] = 0x01
		binary.LittleEndian.PutUint64(header[5:], uint64(len(testData)))

		conn := &mockConn{
			readData: [][]byte{header, testData},
		}

		resp, err := readResponse(conn)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if resp.Response != "success" {
			t.Errorf("Expected success response, got %v", resp.Response)
		}
	})

	t.Run("PartialData", func(t *testing.T) {
		testData := []byte(`{"response":"success"}`)
		header := make([]byte, 13)
		copy(header, "ZBXD")
		header[4] = 0x01
		binary.LittleEndian.PutUint64(header[5:], uint64(len(testData)))

		conn := &mockConn{
			readData: [][]byte{
				header[:5],
				header[5:],
				testData[:10],
				testData[10:],
			},
		}

		resp, err := readResponse(conn)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if resp.Response != "success" {
			t.Errorf("Expected success response, got %v", resp.Response)
		}
	})

	t.Run("ConnectionClosed", func(t *testing.T) {
		conn := &mockConn{readData: [][]byte{}}
		_, err := readResponse(conn)
		if !errors.Is(err, ErrConnectionClosed) {
			t.Errorf("Expected ErrConnectionClosed, got %v", err)
		}
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		header := make([]byte, 13)
		copy(header, "ZBXD")
		header[4] = 0x01
		binary.LittleEndian.PutUint64(header[5:], 10)

		conn := &mockConn{
			readData: [][]byte{header, []byte("{invalid}")},
		}

		_, err := readResponse(conn)
		if !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("Expected ErrInvalidJSON, got %v", err)
		}
	})
}

func TestMisc(t *testing.T) {
	sender := NewSender("localhost", 777)
	t.Run("SetRetries", func(t *testing.T) {
		err := sender.SetRetries(400)
		if err != nil {
			t.Fatalf("SetRetries failed")
		}
		if sender.Retries != 400 {
			t.Fatalf("SetRetries failed, %v != %v", sender.Retries, 400)
		}
	})
	t.Run("SetRetryDelay", func(t *testing.T) {
		err := sender.SetRetryDelay(6 * time.Minute)
		if err != nil {
			t.Fatalf("SetRetryDelay failed")
		}
		if sender.RetryDelay != 6*time.Minute {
			t.Fatalf("SetRetryDelay failed, %v != %v", sender.RetryDelay, 6*time.Minute)
		}
	})
	t.Run("SetTimeout", func(t *testing.T) {
		err := sender.SetTimeout(7 * time.Minute)
		if err != nil {
			t.Fatalf("SetTimeout failed")
		}
		if sender.Timeout != 7*time.Minute {
			t.Fatalf("SetTimeout failed, %v != %v", sender.Timeout, 7*time.Minute)
		}
	})
}

func TestSender(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		server, port := startTestServer(t, func(conn net.Conn) {
			sendSuccessResponse(conn)
		})
		defer server.Close()

		sender := NewSender("localhost", port)
		resp, err := sender.Send([]ZabbixDataItem{{Host: "test", Key: "key"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if resp.Processed != 1 {
			t.Errorf("Expected 1 processed item, got %d", resp.Processed)
		}
	})

	t.Run("SuccessBatches", func(t *testing.T) {
		server, port := startTestServer(t, func(conn net.Conn) {
			sendSuccessResponse(conn)
		})
		defer server.Close()

		sender := NewSender("localhost", port)
		resp, err := sender.SendBatch([]ZabbixDataItem{
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
		}, 3)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(resp) != 2 {
			t.Fatalf("Expected 2 responses, got %d", len(resp))
		}
		for i := range resp {
			if resp[i].Processed != 1 {
				t.Errorf("Expected 1 processed item, got %d", resp[i].Processed)
			}
		}
		resp, err = sender.SendBatch([]ZabbixDataItem{
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
		}, 1)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(resp) != 5 {
			t.Fatalf("Expected 5 responses, got %d", len(resp))
		}
		for i := range resp {
			if resp[i].Processed != 1 {
				t.Errorf("Expected 1 processed item, got %d", resp[i].Processed)
			}
		}
	})

	t.Run("FailedBatches", func(t *testing.T) {
		sender := NewSender("localhost", 8910)
		resp, err := sender.SendBatch([]ZabbixDataItem{
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
			{Host: "test", Key: "key"},
		}, 3)
		if err == nil {
			t.Fatalf("Expected error, got nil")
		}
		if len(resp) != 1 {
			t.Fatalf("Expected 1 response, got %d", len(resp))
		}
	})

	t.Run("RetrySuccess", func(t *testing.T) {
		attempts := 0
		server, port := startTestServer(t, func(conn net.Conn) {
			attempts++
			if attempts < 2 {
				// Первая попытка - закрываем соединение сразу
				conn.Close()
				return
			}
			// Вторая попытка - успешный ответ
			sendSuccessResponse(conn)
		})
		defer server.Close()

		sender := NewSender("localhost", port)
		sender.Retries = 2
		sender.RetryDelay = 10 * time.Millisecond
		sender.Timeout = 100 * time.Millisecond

		_, err := sender.Send([]ZabbixDataItem{{Host: "test", Key: "key"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if attempts != 2 {
			t.Errorf("Expected 2 attempts, got %d", attempts)
		}
	})

	t.Run("Timeout", func(t *testing.T) {
		server, port := startTestServer(t, func(conn net.Conn) {
			time.Sleep(100 * time.Millisecond)
			conn.Read(make([]byte, 1024))
		})
		defer server.Close()

		sender := NewSender("localhost", port)
		sender.Timeout = 50 * time.Millisecond

		_, err := sender.Send([]ZabbixDataItem{{Host: "test", Key: "key"}})
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Errorf("Expected timeout error, got %v", err)
		}
	})

	t.Run("Concurrent", func(t *testing.T) {
		server, port := startTestServer(t, func(conn net.Conn) {
			sendSuccessResponse(conn)
		})
		defer server.Close()

		sender := NewSender("localhost", port)
		var wg sync.WaitGroup
		count := 10

		for i := 0; i < count; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := sender.Send([]ZabbixDataItem{{Host: "test", Key: "key"}})
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}()
		}
		wg.Wait()
	})
}
