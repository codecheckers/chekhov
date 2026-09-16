package announce

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/png"
	"sync"

	"golang.org/x/image/draw"

	"github.com/codecheckers/chekhov/internal/check"
)

// The attachment: the certificate's first pages as an animated GIF.
const (
	// maxFrames is how many pages the animation shows.
	maxFrames = 4
	// frameDelay is how long each page is shown, in hundredths of a second.
	frameDelay = 200
	// width is how wide the animation is, and fallbackWidth how wide when
	// that is over the instance's limit.
	width, fallbackWidth = 800, 600
)

// DefaultImageLimit is the attachment size to stay under when the instance was
// not asked, well below Mastodon's default of 16 MB.
const DefaultImageLimit = 8 << 20

// An Attachment is the built animation.
type Attachment struct {
	GIF    []byte
	Frames int
}

// GIF builds the animation from cert_1.png, cert_2.png, ... next to the
// certificate's page. When it is over the limit it tries narrower, then
// narrower with a page fewer, and gives up rather than post an attachment the instance will
// refuse.
func GIF(services *check.Services, certificate Certificate, limit int64) (Attachment, error) {
	if limit <= 0 {
		limit = DefaultImageLimit
	}

	pages, err := fetchPages(services, certificate)
	if err != nil {
		return Attachment{}, err
	}
	if len(pages) == 0 {
		return Attachment{}, fmt.Errorf("certificate %s has no page images (cert_1.png) at %s",
			certificate.ID, certificate.Page)
	}

	encoded := encode(pages)
	if int64(len(encoded)) <= limit {
		return Attachment{GIF: encoded, Frames: len(pages)}, nil
	}
	narrower := make([]*image.RGBA, len(pages))
	for i, page := range pages {
		narrower[i] = scale(page, fallbackWidth)
	}
	for _, frames := range []int{len(narrower), len(narrower) - 1} {
		if frames == 0 {
			break
		}
		encoded = encode(narrower[:frames])
		if int64(len(encoded)) <= limit {
			return Attachment{GIF: encoded, Frames: frames}, nil
		}
	}
	return Attachment{}, fmt.Errorf(
		"the certificate animation is %d bytes even at its smallest, over the instance's limit of %d", len(encoded), limit)
}

// fetchPages reads cert_1.png to cert_4.png at the same time, and returns the
// pages up to the first one that is missing. Each page is scaled as soon as it
// is read, so a high-resolution scan is not kept at full size.
//
// Only the downloads run in parallel. Decoding and scaling take one page at a
// time: CatmullRom keeps a float buffer of the target width by the source
// height, about 45 MB for an A4 page, and four of those at once took the
// deployment past its 128 MB and got it killed.
func fetchPages(services *check.Services, certificate Certificate) ([]*image.RGBA, error) {
	type result struct {
		page    *image.RGBA
		missing bool
		err     error
	}
	results := make([]result, maxFrames)
	var wg sync.WaitGroup
	var scaling sync.Mutex
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, err := services.FetchFile(fmt.Sprintf("%scert_%d.png", certificate.Page, i+1))
			switch {
			case check.IsNotFound(err):
				results[i].missing = true
				return
			case err != nil:
				// A timeout is not the end of the certificate, and must not
				// quietly post fewer pages.
				results[i].err = err
				return
			}
			scaling.Lock()
			defer scaling.Unlock()
			page, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				results[i].err = fmt.Errorf("cert_%d.png is not a PNG: %w", i+1, err)
				return
			}
			results[i].page = scale(page, width)
		}()
	}
	wg.Wait()

	var pages []*image.RGBA
	for _, result := range results {
		if result.err != nil {
			return nil, result.err
		}
		if result.missing {
			// The pages end where the first one is missing; a page after a
			// gap is not part of the certificate.
			break
		}
		pages = append(pages, result.page)
	}
	return pages, nil
}

func scale(page image.Image, width int) *image.RGBA {
	bounds := page.Bounds()
	height := bounds.Dy() * width / max(bounds.Dx(), 1)
	scaled := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), page, bounds, draw.Src, nil)
	return scaled
}

// encode quantises the pages and writes them as a looping GIF.
func encode(pages []*image.RGBA) []byte {
	animation := &gif.GIF{LoopCount: 0}
	for _, page := range pages {
		animation.Image = append(animation.Image, quantise(page))
		animation.Delay = append(animation.Delay, frameDelay)
	}
	var out bytes.Buffer
	// Writing to memory, a paletted image within GIF's limits, cannot fail.
	_ = gif.EncodeAll(&out, animation)
	return out.Bytes()
}

// plan9Index maps a colour reduced to five bits per channel to its nearest
// entry in the Plan 9 palette. draw.Draw finds the nearest entry for every
// pixel by comparing all 256, which for four pages of 800 by 1131 pixels is
// most of a second each; the table is computed once, when first needed, so
// that no other command pays for it.
var plan9Index = sync.OnceValue(func() *[1 << 15]uint8 {
	var table [1 << 15]uint8
	p := color.Palette(palette.Plan9)
	for key := range table {
		r, g, b := uint8(key>>10&31), uint8(key>>5&31), uint8(key&31)
		table[key] = uint8(p.Index(color.RGBA{R: r<<3 | r>>2, G: g<<3 | g>>2, B: b<<3 | b>>2, A: 255}))
	}
	return &table
})

// quantise reduces a page to the Plan 9 palette. No dithering: a certificate
// is text on white, and dithered anti-aliasing is noise that makes the file
// larger and the text worse.
func quantise(page *image.RGBA) *image.Paletted {
	table := plan9Index()
	frame := image.NewPaletted(page.Bounds(), palette.Plan9)
	for i, j := 0, 0; i < len(page.Pix); i, j = i+4, j+1 {
		r, g, b := page.Pix[i]>>3, page.Pix[i+1]>>3, page.Pix[i+2]>>3
		frame.Pix[j] = table[uint16(r)<<10|uint16(g)<<5|uint16(b)]
	}
	return frame
}
