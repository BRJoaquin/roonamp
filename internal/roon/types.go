package roon

// Zone represents a Roon playback zone.
type Zone struct {
	ZoneID              string      `json:"zone_id"`
	DisplayName         string      `json:"display_name"`
	State               string      `json:"state"`
	IsNextAllowed       bool        `json:"is_next_allowed"`
	IsPreviousAllowed   bool        `json:"is_previous_allowed"`
	IsPauseAllowed      bool        `json:"is_pause_allowed"`
	IsPlayAllowed       bool        `json:"is_play_allowed"`
	IsSeekAllowed       bool        `json:"is_seek_allowed"`
	Settings            *Settings   `json:"settings,omitempty"`
	NowPlaying          *NowPlaying `json:"now_playing,omitempty"`
	Outputs             []Output    `json:"outputs"`
	QueueItemsRemaining int         `json:"queue_items_remaining"`
	QueueTimeRemaining  int         `json:"queue_time_remaining"`
}

type Settings struct {
	Loop      string `json:"loop"`
	Shuffle   bool   `json:"shuffle"`
	AutoRadio bool   `json:"auto_radio"`
}

type NowPlaying struct {
	SeekPosition int      `json:"seek_position"`
	Length       int      `json:"length"`
	OneLine      LineInfo `json:"one_line"`
	TwoLine      LineInfo `json:"two_line"`
	ThreeLine    LineInfo `json:"three_line"`
	ImageKey     string   `json:"image_key"`
}

type LineInfo struct {
	Line1 string `json:"line1"`
	Line2 string `json:"line2,omitempty"`
	Line3 string `json:"line3,omitempty"`
}

type Output struct {
	OutputID       string          `json:"output_id"`
	DisplayName    string          `json:"display_name"`
	ZoneID         string          `json:"zone_id"`
	Volume         *Volume         `json:"volume,omitempty"`
	SourceControls []SourceControl `json:"source_controls,omitempty"`
}

type SourceControl struct {
	ControlKey      string `json:"control_key"`
	DisplayName     string `json:"display_name"`
	SupportsStandby bool   `json:"supports_standby"`
	Status          string `json:"status"`
}

type Volume struct {
	Type    string  `json:"type"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Value   float64 `json:"value"`
	Step    float64 `json:"step"`
	IsMuted bool    `json:"is_muted"`
}

// Registration payloads

type RegisterRequest struct {
	ExtensionID      string   `json:"extension_id"`
	DisplayName      string   `json:"display_name"`
	DisplayVersion   string   `json:"display_version"`
	Publisher        string   `json:"publisher"`
	Email            string   `json:"email"`
	RequiredServices []string `json:"required_services"`
	OptionalServices []string `json:"optional_services"`
	ProvidedServices []string `json:"provided_services"`
	Token            string   `json:"token,omitempty"`
}

type RegisterResponse struct {
	CoreID           string   `json:"core_id"`
	DisplayName      string   `json:"display_name"`
	Token            string   `json:"token"`
	ProvidedServices []string `json:"provided_services"`
	HTTPPort         int      `json:"http_port"`
}

type InfoResponse struct {
	CoreID         string `json:"core_id"`
	DisplayName    string `json:"display_name"`
	DisplayVersion string `json:"display_version"`
}

// Zone subscription messages

type ZonesSubscribeRequest struct {
	SubscriptionKey string `json:"subscription_key"`
}

type ZonesResponse struct {
	Zones        []Zone   `json:"zones,omitempty"`
	ZonesChanged []Zone   `json:"zones_changed,omitempty"`
	ZonesRemoved []string `json:"zones_removed,omitempty"`
	ZonesAdded   []Zone   `json:"zones_added,omitempty"`
}

// Transport control

type ControlRequest struct {
	ZoneOrOutputID string `json:"zone_or_output_id"`
	Control        string `json:"control"`
}

type SeekRequest struct {
	ZoneOrOutputID string `json:"zone_or_output_id"`
	How            string `json:"how"`
	Seconds        int    `json:"seconds"`
}

type VolumeRequest struct {
	OutputID string  `json:"output_id"`
	How      string  `json:"how"`
	Value    float64 `json:"value"`
}

// Browse API

type BrowseRequest struct {
	Hierarchy        string  `json:"hierarchy"`
	ZoneOrOutputID   string  `json:"zone_or_output_id,omitempty"`
	ItemKey          *string `json:"item_key,omitempty"`
	Input            string  `json:"input,omitempty"`
	PopAll           bool    `json:"pop_all,omitempty"`
	SetDisplayOffset int     `json:"set_display_offset,omitempty"`
}

type BrowseResponse struct {
	Action string    `json:"action"`
	List   *ListInfo `json:"list,omitempty"`
}

type ListInfo struct {
	Title   string `json:"title"`
	Count   int    `json:"count"`
	Level   int    `json:"level"`
	Hint    string `json:"hint,omitempty"`
}

type LoadRequest struct {
	Hierarchy       string `json:"hierarchy"`
	Offset          int    `json:"offset"`
	SetDisplayOffset int   `json:"set_display_offset,omitempty"`
	Count           int    `json:"count"`
}

type LoadResponse struct {
	Items  []BrowseItem `json:"items"`
	Offset int          `json:"offset"`
	List   *ListInfo    `json:"list,omitempty"`
}

type BrowseItem struct {
	Title    string  `json:"title"`
	Subtitle string  `json:"subtitle,omitempty"`
	ItemKey  *string `json:"item_key,omitempty"`
	ImageKey string  `json:"image_key,omitempty"`
	Hint     string  `json:"hint,omitempty"`
}

// SOOD discovery

type DiscoveredCore struct {
	DisplayName string
	IP          string
	HTTPPort    string
	UniqueID    string
	ServiceID   string
}
