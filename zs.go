package zs

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	header         = "ZBXD"
	zabbixVersion  = 1
	defaultTimeout = 5 * time.Second
)

var (
	ErrIncompleteHeader       = errors.New("incomplete header received")
	ErrInvalidProtocolVersion = errors.New("invalid protocol version")
	ErrDataLengthMismatch     = errors.New("data length mismatch")
	ErrEmptyData              = errors.New("empty data")
	ErrInvalidJSON            = errors.New("invalid JSON")
	ErrConnectionClosed       = errors.New("connection closed by server")
	ErrResponseStatus         = errors.New("zabbix response error")
)

type ZabbixDataItem struct {
	Host  string `json:"host"`
	Key   string `json:"key"`
	Value string `json:"value"`
	Clock int64  `json:"clock,omitempty"`
}

type ZabbixResponse struct {
	Response    string `json:"response"`
	Info        string `json:"info"`
	Processed   int    `json:"processed"`
	Failed      int    `json:"failed"`
	Total       int    `json:"total"`
	SpentMillis int    `json:"spent_millis"`
}

type ZabbixRequest struct {
	Request string           `json:"request"`
	Data    []ZabbixDataItem `json:"data"`
}

type SenderConfig struct {
	Server     string
	Port       int
	Timeout    time.Duration
	Retries    int
	RetryDelay time.Duration
}

func NewSender(server string, port int) *SenderConfig {
	return &SenderConfig{
		Server:     server,
		Port:       port,
		Timeout:    defaultTimeout,
		Retries:    2,
		RetryDelay: 1 * time.Second,
	}
}

func (s *SenderConfig) Send(items []ZabbixDataItem) (*ZabbixResponse, error) {
	var lastErr error
	var response *ZabbixResponse

	for attempt := 0; attempt <= s.Retries; attempt++ {
		if attempt > 0 {
			time.Sleep(s.RetryDelay)
		}

		response, lastErr = s.trySend(items)
		if lastErr == nil {
			return response, nil
		}

		if !isRetriableError(lastErr) {
			break
		}
	}

	return nil, fmt.Errorf("after %d attempts: %w", s.Retries+1, lastErr)
}

func (s *SenderConfig) trySend(items []ZabbixDataItem) (*ZabbixResponse, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", s.Server, s.Port), s.Timeout)
	if err != nil {
		return nil, fmt.Errorf("connection error: %w", err)
	}
	defer conn.Close()

	request := ZabbixRequest{
		Request: "sender data",
		Data:    items,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("json marshal error: %w", err)
	}

	packet, err := buildPacket(jsonData)
	if err != nil {
		return nil, fmt.Errorf("packet build error: %w", err)
	}

	if err = conn.SetDeadline(time.Now().Add(s.Timeout)); err != nil {
		return nil, fmt.Errorf("set deadline error: %w", err)
	}

	if _, err = conn.Write(packet); err != nil {
		return nil, fmt.Errorf("write error: %w", err)
	}

	return readResponse(conn)
}

func buildPacket(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, ErrEmptyData
	}

	buffer := new(bytes.Buffer)
	buffer.Write([]byte(header))
	buffer.WriteByte(zabbixVersion)
	binary.Write(buffer, binary.LittleEndian, uint64(len(data)))
	buffer.Write(data)

	return buffer.Bytes(), nil
}

func readResponse(conn net.Conn) (*ZabbixResponse, error) {
	header := make([]byte, 13)
	if _, err := io.ReadFull(conn, header); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrConnectionClosed
		}
		return nil, fmt.Errorf("read header error: %w", err)
	}

	if string(header[:4]) != "ZBXD" {
		return nil, ErrIncompleteHeader
	}

	if header[4] != zabbixVersion {
		return nil, ErrInvalidProtocolVersion
	}

	dataLength := binary.LittleEndian.Uint64(header[5:13])
	if dataLength == 0 {
		return nil, ErrEmptyData
	}

	data := make([]byte, dataLength)
	n, err := io.ReadFull(conn, data)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			// Если данных меньше, чем ожидалось, но что-то есть - пробуем распарсить
			if n > 0 {
				data = data[:n]
			} else {
				return nil, ErrConnectionClosed
			}
		} else {
			return nil, fmt.Errorf("read data error: %w", err)
		}
	}

	var response ZabbixResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}

	if response.Response != "success" {
		return nil, fmt.Errorf("%w: %s", ErrResponseStatus, response.Info)
	}

	return &response, nil
}

func isRetriableError(err error) bool {
	if errors.Is(err, ErrConnectionClosed) ||
		errors.Is(err, ErrIncompleteHeader) ||
		errors.Is(err, io.EOF) ||
		strings.Contains(err.Error(), "connection reset by peer") {
		return true
	}

	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}

	_, ok := err.(*net.OpError)
	return ok
}
