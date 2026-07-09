package roon

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	host     string
	port     string
	httpPort string // from register response, may differ from ws port
	token    string

	connMu sync.RWMutex
	moo    *MooConn

	closing      atomic.Bool
	reconnecting atomic.Bool
	connected    atomic.Bool

	mu             sync.RWMutex
	zones          map[string]*Zone
	onZonesUpdated func(zones map[string]*Zone)
}

// SetOnZonesUpdated installs the callback invoked with a fresh zone snapshot
// after every zone update. The callback runs on the read-loop goroutine, so it
// must not block and must not touch UI state directly.
func (c *Client) SetOnZonesUpdated(fn func(zones map[string]*Zone)) {
	c.mu.Lock()
	c.onZonesUpdated = fn
	c.mu.Unlock()
}

// Connected reports whether the WebSocket link is currently up. It flips false
// while the client is reconnecting after a dropped connection.
func (c *Client) Connected() bool { return c.connected.Load() }

// conn returns the current connection under a read lock. It may be replaced
// during reconnection, so callers should fetch it fresh per operation.
func (c *Client) conn() *MooConn {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.moo
}

func (c *Client) setConn(m *MooConn) {
	c.connMu.Lock()
	c.moo = m
	c.connMu.Unlock()
}

func NewClient(host, port, token string) *Client {
	return &Client{
		host:  host,
		port:  port,
		token: token,
		zones: make(map[string]*Zone),
	}
}

func (c *Client) Connect() error {
	if err := c.dial(); err != nil {
		return err
	}
	go c.runReadLoop(c.conn())
	return nil
}

// dial opens a fresh WebSocket and installs it as the current connection,
// including the ping responder for Roon's keepalive pings.
func (c *Client) dial() error {
	u := url.URL{Scheme: "ws", Host: c.host + ":" + c.port, Path: "/api"}
	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("websocket dial %s: %w", u.String(), err)
	}

	moo := NewMooConn(ws)
	moo.onRequest = func(msg *MooMessage) {
		if msg.Name == "com.roonlabs.ping:1/ping" {
			moo.SendResponse(msg.RequestID, "Success")
		}
	}
	c.setConn(moo)
	c.connected.Store(true)
	return nil
}

// runReadLoop owns one connection's read loop. When it ends unexpectedly it
// kicks off reconnection. Exactly one read loop is active per connection.
func (c *Client) runReadLoop(moo *MooConn) {
	err := moo.ReadLoop()
	log.Printf("read loop ended: %v", err)
	c.connected.Store(false)

	if c.closing.Load() {
		return
	}
	// Guard against multiple concurrent reconnect drivers.
	if c.reconnecting.CompareAndSwap(false, true) {
		c.reconnectLoop()
	}
}

// reconnectLoop re-dials, re-registers, and re-subscribes with exponential
// backoff until it succeeds or the client is closing. Re-subscribing fires
// OnZonesUpdated, which refreshes the UI without a manual restart.
func (c *Client) reconnectLoop() {
	defer c.reconnecting.Store(false)

	backoff := time.Second
	for {
		if c.closing.Load() {
			return
		}
		time.Sleep(backoff)
		if c.closing.Load() {
			return
		}

		if err := c.reestablish(); err != nil {
			log.Printf("reconnect attempt failed: %v", err)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		log.Printf("reconnected to Roon Core")
		return
	}
}

// reestablish performs one full reconnect: dial, start the read loop, then
// re-register (with the saved token) and re-subscribe to zones.
func (c *Client) reestablish() error {
	if err := c.dial(); err != nil {
		return err
	}
	moo := c.conn()
	go c.runReadLoop(moo)

	if _, err := c.Register(); err != nil {
		moo.Close()
		return fmt.Errorf("re-register: %w", err)
	}
	if err := c.SubscribeZones(); err != nil {
		moo.Close()
		return fmt.Errorf("re-subscribe zones: %w", err)
	}
	return nil
}

func (c *Client) GetInfo() (*InfoResponse, error) {
	resp, err := c.conn().Send("com.roonlabs.registry:1/info", nil)
	if err != nil {
		return nil, err
	}
	var info InfoResponse
	if err := json.Unmarshal(resp.Body, &info); err != nil {
		return nil, fmt.Errorf("unmarshal info: %w", err)
	}
	return &info, nil
}

func (c *Client) Register() (*RegisterResponse, error) {
	req := RegisterRequest{
		ExtensionID:      "com.brokenrubik.roonamp",
		DisplayName:      "roonamp",
		DisplayVersion:   "0.1.0",
		Publisher:        "BrokenRubik",
		Email:            "dev@brokenrubik.com",
		RequiredServices: []string{"com.roonlabs.transport:2", "com.roonlabs.browse:1", "com.roonlabs.image:1"},
		OptionalServices: []string{},
		ProvidedServices: []string{"com.roonlabs.ping:1"},
		Token:            c.token,
	}

	// First-time registration (no token yet) blocks until the user enables
	// the extension in Roon Settings -> Extensions, so it must wait without a
	// timeout. Re-registration with a saved token answers immediately; the
	// timeout there keeps a lost reply from hanging the reconnect loop.
	timeout := requestTimeout
	if c.token == "" {
		timeout = 0
	}
	resp, err := c.conn().Subscribe("com.roonlabs.registry:1/register", req, func(msg *MooMessage) {
		log.Printf("register update: %s", string(msg.Body))
	}, timeout)
	if err != nil {
		return nil, err
	}

	var reg RegisterResponse
	if err := json.Unmarshal(resp.Body, &reg); err != nil {
		return nil, fmt.Errorf("unmarshal register: %w", err)
	}

	c.token = reg.Token
	if reg.HTTPPort > 0 {
		c.httpPort = fmt.Sprintf("%d", reg.HTTPPort)
	}
	return &reg, nil
}

func (c *Client) Token() string { return c.token }
func (c *Client) Host() string  { return c.host }
func (c *Client) Port() string  { return c.port }

// ImagePort returns the HTTP port for image requests (may differ from WS port).
func (c *Client) ImagePort() string {
	if c.httpPort != "" {
		return c.httpPort
	}
	return c.port
}

func (c *Client) Zones() map[string]*Zone {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked()
}

// snapshotLocked copies the zone map. Callers must hold c.mu.
func (c *Client) snapshotLocked() map[string]*Zone {
	snapshot := make(map[string]*Zone, len(c.zones))
	for k, v := range c.zones {
		snapshot[k] = v
	}
	return snapshot
}

func (c *Client) SubscribeZones() error {
	req := ZonesSubscribeRequest{SubscriptionKey: "0"}
	_, err := c.conn().Subscribe("com.roonlabs.transport:2/subscribe_zones", req, func(msg *MooMessage) {
		c.handleZoneUpdate(msg)
	}, requestTimeout)
	return err
}

func (c *Client) handleZoneUpdate(msg *MooMessage) {
	var resp ZonesResponse
	if err := json.Unmarshal(msg.Body, &resp); err != nil {
		log.Printf("zone update unmarshal: %v", err)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if resp.Zones != nil {
		c.zones = make(map[string]*Zone)
		for i := range resp.Zones {
			z := resp.Zones[i]
			c.zones[z.ZoneID] = &z
		}
	}
	for i := range resp.ZonesAdded {
		z := resp.ZonesAdded[i]
		c.zones[z.ZoneID] = &z
	}
	for i := range resp.ZonesChanged {
		z := resp.ZonesChanged[i]
		c.zones[z.ZoneID] = &z
	}
	for _, id := range resp.ZonesRemoved {
		delete(c.zones, id)
	}

	if c.onZonesUpdated != nil {
		c.onZonesUpdated(c.snapshotLocked())
	}
}

// -- Transport controls --

func (c *Client) Control(zoneID, control string) error {
	_, err := c.conn().Send("com.roonlabs.transport:2/control",
		ControlRequest{ZoneOrOutputID: zoneID, Control: control})
	return err
}

func (c *Client) ChangeVolume(outputID, how string, value float64) error {
	_, err := c.conn().Send("com.roonlabs.transport:2/change_volume",
		VolumeRequest{OutputID: outputID, How: how, Value: value})
	return err
}

// -- Browse --

func (c *Client) Browse(req BrowseRequest) (*BrowseResponse, error) {
	resp, err := c.conn().Send("com.roonlabs.browse:1/browse", req)
	if err != nil {
		return nil, fmt.Errorf("browse: %w", err)
	}
	var br BrowseResponse
	if err := json.Unmarshal(resp.Body, &br); err != nil {
		return nil, fmt.Errorf("unmarshal browse: %w", err)
	}
	return &br, nil
}

func (c *Client) Load(req LoadRequest) (*LoadResponse, error) {
	resp, err := c.conn().Send("com.roonlabs.browse:1/load", req)
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	var lr LoadResponse
	if err := json.Unmarshal(resp.Body, &lr); err != nil {
		return nil, fmt.Errorf("unmarshal load: %w", err)
	}
	return &lr, nil
}

func (c *Client) Close() error {
	c.closing.Store(true)
	if moo := c.conn(); moo != nil {
		return moo.Close()
	}
	return nil
}
