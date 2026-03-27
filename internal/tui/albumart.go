package tui

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"time"

	"roonamp/internal/roon"

	"github.com/charmbracelet/lipgloss"
)

const (
	artWidth  = 24 // chars wide = pixels wide
	artHeight = 12 // chars tall = 24 pixels tall (half-blocks)
)

const halfBlock = "\u2580" // ▀ upper half block

var imageHTTPClient = &http.Client{Timeout: 5 * time.Second}

// FetchAndRenderArt fetches album art via HTTP from the Roon Core.
func FetchAndRenderArt(client *roon.Client, imageKey string) (string, error) {
	if imageKey == "" {
		return renderPlaceholder(), nil
	}

	pixW := artWidth * 8
	pixH := artHeight * 8

	// Try the WebSocket port first, then the HTTP port from register
	img, err := fetchImageHTTP(client.Host(), client.Port(), imageKey, pixW, pixH)
	if err != nil && client.ImagePort() != client.Port() {
		img, err = fetchImageHTTP(client.Host(), client.ImagePort(), imageKey, pixW, pixH)
	}
	if err != nil {
		return renderPlaceholder(), err
	}

	return renderImage(img, artWidth, artHeight), nil
}

func fetchImageHTTP(host, port, imageKey string, w, h int) (image.Image, error) {
	url := fmt.Sprintf("http://%s:%s/api/image/%s?scale=fit&width=%d&height=%d",
		host, port, imageKey, w, h)

	resp, err := imageHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return img, nil
}

// renderImage converts an image to terminal half-block art.
// Uses area-average sampling for smooth downscaling.
func renderImage(img image.Image, w, h int) string {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	pixW := w
	pixH := h * 2

	var sb strings.Builder

	for row := 0; row < pixH; row += 2 {
		if row > 0 {
			sb.WriteRune('\n')
		}
		for col := 0; col < pixW; col++ {
			// Top pixel (area average)
			r1, g1, b1 := sampleArea(img, bounds,
				col*srcW/pixW, row*srcH/pixH,
				(col+1)*srcW/pixW, (row+1)*srcH/pixH)

			// Bottom pixel (area average)
			r2, g2, b2 := sampleArea(img, bounds,
				col*srcW/pixW, (row+1)*srcH/pixH,
				(col+1)*srcW/pixW, (row+2)*srcH/pixH)

			fg := fmt.Sprintf("#%02x%02x%02x", r1, g1, b1)
			bg := fmt.Sprintf("#%02x%02x%02x", r2, g2, b2)

			style := lipgloss.NewStyle().
				Foreground(lipgloss.Color(fg)).
				Background(lipgloss.Color(bg))

			sb.WriteString(style.Render(halfBlock))
		}
	}

	return sb.String()
}

// sampleArea averages all pixels in the given rectangle for smooth downscaling.
func sampleArea(img image.Image, bounds image.Rectangle, x0, y0, x1, y1 int) (uint8, uint8, uint8) {
	x0 += bounds.Min.X
	y0 += bounds.Min.Y
	x1 += bounds.Min.X
	y1 += bounds.Min.Y

	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	if x1 > bounds.Max.X {
		x1 = bounds.Max.X
	}
	if y1 > bounds.Max.Y {
		y1 = bounds.Max.Y
	}

	var rSum, gSum, bSum uint64
	count := uint64(0)

	for sy := y0; sy < y1; sy++ {
		for sx := x0; sx < x1; sx++ {
			r, g, b, _ := img.At(sx, sy).RGBA()
			rSum += uint64(r)
			gSum += uint64(g)
			bSum += uint64(b)
			count++
		}
	}

	if count == 0 {
		return 0, 0, 0
	}

	return uint8(rSum / count >> 8), uint8(gSum / count >> 8), uint8(bSum / count >> 8)
}

func renderPlaceholder() string {
	top := styleDim.Render("+" + strings.Repeat("-", artWidth-2) + "+")

	var lines []string
	lines = append(lines, top)

	emptyRows := artHeight - 2
	midRow := emptyRows / 2

	for i := 0; i < emptyRows; i++ {
		if i == midRow {
			label := "no art"
			pad := (artWidth - 2 - len(label)) / 2
			content := strings.Repeat(" ", pad) + label + strings.Repeat(" ", artWidth-2-pad-len(label))
			lines = append(lines, styleDim.Render("|")+styleDim.Render(content)+styleDim.Render("|"))
		} else {
			lines = append(lines, styleDim.Render("|")+strings.Repeat(" ", artWidth-2)+styleDim.Render("|"))
		}
	}

	lines = append(lines, top)
	return strings.Join(lines, "\n")
}
