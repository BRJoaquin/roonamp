package roon

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
)

type Client struct {
	moo      *MooConn
	host     string
	port     string
	httpPort string // from register response, may differ from ws port
	token    string

	mu    sync.RWMutex
	zones map[string]*Zone

	OnZonesUpdated func(zones map[string]*Zone)
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
	u := url.URL{Scheme: "ws", Host: c.host + ":" + c.port, Path: "/api"}
	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("websocket dial %s: %w", u.String(), err)
	}

	c.moo = NewMooConn(ws)
	c.moo.onRequest = func(msg *MooMessage) {
		if msg.Name == "com.roonlabs.ping:1/ping" {
			c.moo.SendResponse(msg.RequestID, "Success")
		}
	}

	go func() {
		if err := c.moo.ReadLoop(); err != nil {
			log.Printf("read loop ended: %v", err)
		}
	}()

	return nil
}

func (c *Client) GetInfo() (*InfoResponse, error) {
	resp, err := c.moo.Send("com.roonlabs.registry:1/info", nil)
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
		RequiredServices: []string{"com.roonlabs.transport:2"},
		OptionalServices: []string{"com.roonlabs.browse:1", "com.roonlabs.image:1"},
		ProvidedServices: []string{"com.roonlabs.ping:1"},
		Token:            c.token,
	}

	resp, err := c.moo.Subscribe("com.roonlabs.registry:1/register", req, func(msg *MooMessage) {
		log.Printf("register update: %s", string(msg.Body))
	})
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

// ImagePort returns the HTTP port for image requests (may differ from WS port).
func (c *Client) ImagePort() string {
	if c.httpPort != "" {
		return c.httpPort
	}
	return c.port
}

func (c *Client) SubscribeZones() error {
	req := ZonesSubscribeRequest{SubscriptionKey: "0"}
	_, err := c.moo.Subscribe("com.roonlabs.transport:2/subscribe_zones", req, func(msg *MooMessage) {
		c.handleZoneUpdate(msg)
	})
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
	for i := range resp.ZonesRemoved {
		delete(c.zones, resp.ZonesRemoved[i].ZoneID)
	}

	if c.OnZonesUpdated != nil {
		snapshot := make(map[string]*Zone, len(c.zones))
		for k, v := range c.zones {
			snapshot[k] = v
		}
		c.OnZonesUpdated(snapshot)
	}
}

// -- Transport controls --

func (c *Client) Control(zoneID, control string) error {
	_, err := c.moo.Send("com.roonlabs.transport:2/control",
		ControlRequest{ZoneOrOutputID: zoneID, Control: control})
	return err
}

func (c *Client) ChangeVolume(outputID, how string, value float64) error {
	_, err := c.moo.Send("com.roonlabs.transport:2/change_volume",
		VolumeRequest{OutputID: outputID, How: how, Value: value})
	return err
}

func (c *Client) Seek(zoneID, how string, seconds int) error {
	_, err := c.moo.Send("com.roonlabs.transport:2/seek",
		SeekRequest{ZoneOrOutputID: zoneID, How: how, Seconds: seconds})
	return err
}

// -- Image --

func (c *Client) GetImage(imageKey string, width, height int) ([]byte, error) {
	req := map[string]interface{}{
		"image_key": imageKey,
		"scale":     "fit",
		"width":     width,
		"height":    height,
		"format":    "image/jpeg",
	}
	resp, err := c.moo.Send("com.roonlabs.image:1/get_image", req)
	if err != nil {
		return nil, fmt.Errorf("get_image: %w", err)
	}
	if len(resp.RawBody) > 0 {
		return resp.RawBody, nil
	}
	if len(resp.Body) > 0 {
		return []byte(resp.Body), nil
	}
	return nil, fmt.Errorf("empty image response")
}

func (c *Client) Close() error {
	if c.moo != nil {
		return c.moo.Close()
	}
	return nil
}
