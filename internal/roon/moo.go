package roon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// MooConn handles MOO protocol messaging over WebSocket.
type MooConn struct {
	ws          *websocket.Conn
	reqID       atomic.Int64
	mu          sync.Mutex
	handlers    map[int64]chan *MooMessage
	subHandlers map[int64]func(*MooMessage)
	handlerMu   sync.Mutex
	onRequest   func(*MooMessage)
}

type MooMessage struct {
	Verb        string // REQUEST, COMPLETE, CONTINUE
	RequestID   int64
	Name        string // service/method path or status name
	ContentType string // e.g. "application/json", "image/jpeg"
	Body        json.RawMessage
	RawBody     []byte // binary body (for non-JSON responses like images)
}

func NewMooConn(ws *websocket.Conn) *MooConn {
	return &MooConn{
		ws:          ws,
		handlers:    make(map[int64]chan *MooMessage),
		subHandlers: make(map[int64]func(*MooMessage)),
	}
}

// Send sends a MOO REQUEST and waits for the COMPLETE/CONTINUE response.
func (m *MooConn) Send(servicePath string, body interface{}) (*MooMessage, error) {
	id := m.reqID.Add(1)
	ch := make(chan *MooMessage, 1)

	m.handlerMu.Lock()
	m.handlers[id] = ch
	m.handlerMu.Unlock()

	if err := m.writeRequest(id, servicePath, body); err != nil {
		return nil, err
	}

	msg := <-ch
	return msg, nil
}

// Subscribe sends a MOO REQUEST and calls handler on every CONTINUE.
// Returns the first CONTINUE message (initial state).
func (m *MooConn) Subscribe(servicePath string, body interface{}, handler func(*MooMessage)) (*MooMessage, error) {
	id := m.reqID.Add(1)
	ch := make(chan *MooMessage, 1)

	m.handlerMu.Lock()
	m.handlers[id] = ch
	m.subHandlers[id] = handler
	m.handlerMu.Unlock()

	if err := m.writeRequest(id, servicePath, body); err != nil {
		return nil, err
	}

	msg := <-ch
	return msg, nil
}

// MOO/1 wire format:
//   MOO/1 REQUEST {service/method}\n
//   Request-Id: {id}\n
//   Content-Type: application/json\n   (only if body)
//   Content-Length: {n}\n               (only if body)
//   \n
//   {json_body}                         (only if body)

func (m *MooConn) writeRequest(id int64, servicePath string, body interface{}) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "MOO/1 REQUEST %s\nRequest-Id: %d\n", servicePath, id)

	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		fmt.Fprintf(&buf, "Content-Type: application/json\nContent-Length: %d\n", len(payload))
		buf.WriteByte('\n')
		buf.Write(payload)
	} else {
		buf.WriteByte('\n')
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ws.WriteMessage(websocket.BinaryMessage, buf.Bytes())
}

// SendResponse sends a COMPLETE response (for answering Roon's requests like ping).
func (m *MooConn) SendResponse(requestID int64, status string) error {
	msg := fmt.Sprintf("MOO/1 COMPLETE %s\nRequest-Id: %d\n\n", status, requestID)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ws.WriteMessage(websocket.BinaryMessage, []byte(msg))
}

// ReadLoop reads messages from the WebSocket and dispatches them.
func (m *MooConn) ReadLoop() error {
	for {
		_, data, err := m.ws.ReadMessage()
		if err != nil {
			return fmt.Errorf("ws read: %w", err)
		}

		msg, err := parseMooMessage(data)
		if err != nil {
			continue
		}

		switch msg.Verb {
		case "REQUEST":
			if m.onRequest != nil {
				m.onRequest(msg)
			}

		case "COMPLETE":
			m.handlerMu.Lock()
			ch, ok := m.handlers[msg.RequestID]
			if ok {
				delete(m.handlers, msg.RequestID)
				delete(m.subHandlers, msg.RequestID)
			}
			m.handlerMu.Unlock()
			if ok {
				ch <- msg
			}

		case "CONTINUE":
			m.handlerMu.Lock()
			ch, hasCh := m.handlers[msg.RequestID]
			subFn, hasSub := m.subHandlers[msg.RequestID]
			if hasCh {
				delete(m.handlers, msg.RequestID)
			}
			m.handlerMu.Unlock()

			if hasCh {
				ch <- msg
			}
			if hasSub {
				subFn(msg)
			}
		}
	}
}

// parseMooMessage parses the MOO/1 wire format from raw bytes.
// Handles both JSON and binary bodies (e.g. image data).
func parseMooMessage(data []byte) (*MooMessage, error) {
	// Split headers from body at the first \n\n boundary.
	// We work with raw bytes to preserve binary body data.
	sep := []byte("\n\n")
	idx := bytes.Index(data, sep)
	if idx < 0 {
		return nil, fmt.Errorf("no header/body separator")
	}

	headerSection := string(data[:idx])
	body := data[idx+2:] // everything after \n\n

	headerLines := strings.Split(headerSection, "\n")
	if len(headerLines) == 0 {
		return nil, fmt.Errorf("no header line")
	}

	// First line: MOO/1 VERB name
	fields := strings.SplitN(headerLines[0], " ", 3)
	if len(fields) < 3 {
		return nil, fmt.Errorf("malformed first line: %s", headerLines[0])
	}

	msg := &MooMessage{
		Verb: fields[1],
		Name: fields[2],
	}

	// Parse headers
	for _, line := range headerLines[1:] {
		if strings.HasPrefix(line, "Request-Id: ") {
			idStr := strings.TrimPrefix(line, "Request-Id: ")
			reqID, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("bad request id: %w", err)
			}
			msg.RequestID = reqID
		} else if strings.HasPrefix(line, "Content-Type: ") {
			msg.ContentType = strings.TrimPrefix(line, "Content-Type: ")
		}
	}

	// Store body based on content type
	if len(body) > 0 {
		if msg.ContentType != "" && msg.ContentType != "application/json" {
			// Binary body (images, etc.) -- keep raw bytes
			msg.RawBody = make([]byte, len(body))
			copy(msg.RawBody, body)
		} else {
			// JSON body
			msg.Body = json.RawMessage(body)
		}
	}

	return msg, nil
}

func (m *MooConn) Close() error {
	return m.ws.Close()
}
