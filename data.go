// Copyright 2013 The imageproxy authors.
// SPDX-License-Identifier: Apache-2.0

package imageproxy

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	optFit             = "fit"
	optFlipVertical    = "fv"
	optFlipHorizontal  = "fh"
	optFormatJPEG      = "jpeg"
	optFormatPNG       = "png"
	optFormatTIFF      = "tiff"
	optRotatePrefix    = "r"
	optQualityPrefix   = "q"
	optSignaturePrefix = "s"
	optSizeDelimiter   = "x"
	optScaleUp         = "scaleUp"
	optCropX           = "cx"
	optCropY           = "cy"
	optCropWidth       = "cw"
	optCropHeight      = "ch"
	optSmartCrop       = "sc"
	optTrim            = "trim"
	optValidUntil      = "vu"
)

// URLError reports a malformed URL error.
type URLError struct {
	Message string
	URL     *url.URL
}

func (e URLError) Error() string {
	return fmt.Sprintf("malformed URL %q: %s", e.URL, e.Message)
}

// Options specifies transformations to be performed on the requested image.
type Options struct {
	// See ParseOptions for interpretation of Width and Height values
	Width  float64
	Height float64

	// If true, resize the image to fit in the specified dimensions.  Image
	// will not be cropped, and aspect ratio will be maintained.
	Fit bool

	// Rotate image the specified degrees counter-clockwise.  Valid values
	// are 90, 180, 270.
	Rotate int

	FlipVertical   bool
	FlipHorizontal bool

	// Quality of output image
	Quality int

	// HMAC Signature for signed requests.
	Signature string

	// Allow image to scale beyond its original dimensions.  This value
	// will always be overwritten by the value of Proxy.ScaleUp.
	ScaleUp bool

	// Desired image format. Valid values are "jpeg", "png", "tiff".
	Format string

	// Crop rectangle params
	CropX      float64
	CropY      float64
	CropWidth  float64
	CropHeight float64

	// Automatically find good crop points based on image content.
	SmartCrop bool

	// If true, automatically trim pixels of the same color around the edges
	Trim bool

	// If non-zero, the URL is valid until this time.
	ValidUntil time.Time
}

func (o Options) String() string {
	opts := []string{fmt.Sprintf("%v%s%v", o.Width, optSizeDelimiter, o.Height)}
	if o.Fit {
		opts = append(opts, optFit)
	}
	if o.Rotate != 0 {
		opts = append(opts, fmt.Sprintf("%s%d", optRotatePrefix, o.Rotate))
	}
	if o.FlipVertical {
		opts = append(opts, optFlipVertical)
	}
	if o.FlipHorizontal {
		opts = append(opts, optFlipHorizontal)
	}
	if o.Quality != 0 {
		opts = append(opts, fmt.Sprintf("%s%d", optQualityPrefix, o.Quality))
	}
	if o.Signature != "" {
		opts = append(opts, fmt.Sprintf("%s%s", optSignaturePrefix, o.Signature))
	}
	if o.ScaleUp {
		opts = append(opts, optScaleUp)
	}
	if o.Format != "" {
		opts = append(opts, o.Format)
	}
	if o.CropX != 0 {
		opts = append(opts, fmt.Sprintf("%s%v", optCropX, o.CropX))
	}
	if o.CropY != 0 {
		opts = append(opts, fmt.Sprintf("%s%v", optCropY, o.CropY))
	}
	if o.CropWidth != 0 {
		opts = append(opts, fmt.Sprintf("%s%v", optCropWidth, o.CropWidth))
	}
	if o.CropHeight != 0 {
		opts = append(opts, fmt.Sprintf("%s%v", optCropHeight, o.CropHeight))
	}
	if o.SmartCrop {
		opts = append(opts, optSmartCrop)
	}
	if o.Trim {
		opts = append(opts, optTrim)
	}
	if !o.ValidUntil.IsZero() {
		opts = append(opts, fmt.Sprintf("%s%d", optValidUntil, o.ValidUntil.Unix()))
	}

	sort.Strings(opts)

	return strings.Join(opts, ",")
}

// transform returns whether o includes transformation options.  Some fields
// are not transform related at all (like Signature), and others only apply in
// the presence of other fields (like Fit).  A non-empty Format value is
// assumed to involve a transformation.
func (o Options) transform() bool {
	return o.Width != 0 || o.Height != 0 || o.Rotate != 0 || o.FlipHorizontal || o.FlipVertical || o.Quality != 0 || o.Format != "" || o.CropX != 0 || o.CropY != 0 || o.CropWidth != 0 || o.CropHeight != 0 || o.Trim
}

// ParseOptions parses str as a list of comma separated transformation options.
// The options can be specified in in order, with duplicate options overwriting
// previous values.
//
// # Rectangle Crop
//
// There are four options controlling rectangle crop:
//
//	cx{x}      - X coordinate of top left rectangle corner (default: 0)
//	cy{y}      - Y coordinate of top left rectangle corner (default: 0)
//	cw{width}  - rectangle width (default: image width)
//	ch{height} - rectangle height (default: image height)
//
// For all options, integer values are interpreted as exact pixel values and
// floats between 0 and 1 are interpreted as percentages of the original image
// size. Negative values for cx and cy are measured from the right and bottom
// edges of the image, respectively.
//
// If the crop width or height exceed the width or height of the image, the
// crop width or height will be adjusted, preserving the specified cx and cy
// values.  Rectangular crop is applied before any other transformations.
//
// # Smart Crop
//
// The "sc" option will perform a content-aware smart crop to fit the
// requested image width and height dimensions (see Size and Cropping below).
// The smart crop option will override any requested rectangular crop.
//
// # Size and Cropping
//
// The size option takes the general form "{width}x{height}", where width and
// height are numbers. Integer values greater than 1 are interpreted as exact
// pixel values. Floats between 0 and 1 are interpreted as percentages of the
// original image size. If either value is omitted or set to 0, it will be
// automatically set to preserve the aspect ratio based on the other dimension.
// If a single number is provided (with no "x" separator), it will be used for
// both height and width.
//
// Depending on the size options specified, an image may be cropped to fit the
// requested size. In all cases, the original aspect ratio of the image will be
// preserved; imageproxy will never stretch the original image.
//
// When no explicit crop mode is specified, the following rules are followed:
//
// - If both width and height values are specified, the image will be scaled to
// fill the space, cropping if necessary to fit the exact dimension.
//
// - If only one of the width or height values is specified, the image will be
// resized to fit the specified dimension, scaling the other dimension as
// needed to maintain the aspect ratio.
//
// If the "fit" option is specified together with a width and height value, the
// image will be resized to fit within a containing box of the specified size.
// As always, the original aspect ratio will be preserved. Specifying the "fit"
// option with only one of either width or height does the same thing as if
// "fit" had not been specified.
//
// # Rotation and Flips
//
// The "r{degrees}" option will rotate the image the specified number of
// degrees, counter-clockwise. Valid degrees values are 90, 180, and 270.
//
// The "fv" option will flip the image vertically. The "fh" option will flip
// the image horizontally. Images are flipped after being rotated.
//
// # Quality
//
// The "q{qualityPercentage}" option can be used to specify the quality of the
// output file (JPEG only). If not specified, the default value of "95" is used.
//
// # Format
//
// The "jpeg", "png", and "tiff" options can be used to specify the desired
// image format of the proxied image.
//
// # Signature
//
// The "s{signature}" option specifies an optional base64 encoded HMAC used to
// sign the remote URL in the request.  The HMAC key used to verify signatures is
// provided to the imageproxy server on startup.
//
// See https://github.com/willnorris/imageproxy/blob/master/docs/url-signing.md
// for examples of generating signatures.
//
// # Trim
//
// The "trim" option will automatically trim pixels of the same color around
// the edges of the image.  This is useful for removing borders from images
// that have been resized or cropped.  The trim option is applied after any
// cropping or resizing has been performed.
//
// Examples
//
//	0x0         - no resizing
//	200x        - 200 pixels wide, proportional height
//	x0.15       - 15% original height, proportional width
//	100x150     - 100 by 150 pixels, cropping as needed
//	100         - 100 pixels square, cropping as needed
//	150,fit     - scale to fit 150 pixels square, no cropping
//	100,r90     - 100 pixels square, rotated 90 degrees
//	100,fv,fh   - 100 pixels square, flipped horizontal and vertical
//	200x,q60    - 200 pixels wide, proportional height, 60% quality
//	200x,png    - 200 pixels wide, converted to PNG format
//	cw100,ch100 - crop image to 100px square, starting at (0,0)
//	cx10,cy20,cw100,ch200 - crop image starting at (10,20) is 100px wide and 200px tall
func ParseOptions(str string) Options {
	var options Options

	for _, opt := range strings.Split(str, ",") {
		switch {
		case len(opt) == 0: // do nothing
		case opt == optFit:
			options.Fit = true
		case opt == optFlipVertical:
			options.FlipVertical = true
		case opt == optFlipHorizontal:
			options.FlipHorizontal = true
		case opt == optScaleUp: // this option is intentionally not documented above
			options.ScaleUp = true
		case opt == optFormatJPEG, opt == optFormatPNG, opt == optFormatTIFF:
			options.Format = opt
		case opt == optSmartCrop:
			options.SmartCrop = true
		case opt == optTrim:
			options.Trim = true
		case strings.HasPrefix(opt, optRotatePrefix):
			value := strings.TrimPrefix(opt, optRotatePrefix)
			options.Rotate, _ = strconv.Atoi(value)
		case strings.HasPrefix(opt, optQualityPrefix):
			value := strings.TrimPrefix(opt, optQualityPrefix)
			options.Quality, _ = strconv.Atoi(value)
		case strings.HasPrefix(opt, optSignaturePrefix):
			options.Signature = strings.TrimPrefix(opt, optSignaturePrefix)
		case strings.HasPrefix(opt, optCropX):
			value := strings.TrimPrefix(opt, optCropX)
			options.CropX, _ = strconv.ParseFloat(value, 64)
		case strings.HasPrefix(opt, optCropY):
			value := strings.TrimPrefix(opt, optCropY)
			options.CropY, _ = strconv.ParseFloat(value, 64)
		case strings.HasPrefix(opt, optCropWidth):
			value := strings.TrimPrefix(opt, optCropWidth)
			options.CropWidth, _ = strconv.ParseFloat(value, 64)
		case strings.HasPrefix(opt, optCropHeight):
			value := strings.TrimPrefix(opt, optCropHeight)
			options.CropHeight, _ = strconv.ParseFloat(value, 64)
		case strings.HasPrefix(opt, optValidUntil):
			value := strings.TrimPrefix(opt, optValidUntil)
			if v, _ := strconv.ParseInt(value, 10, 64); v > 0 {
				options.ValidUntil = time.Unix(v, 0)
			}
		case strings.Contains(opt, optSizeDelimiter):
			size := strings.SplitN(opt, optSizeDelimiter, 2)
			if w := size[0]; w != "" {
				options.Width, _ = strconv.ParseFloat(w, 64)
			}
			if h := size[1]; h != "" {
				options.Height, _ = strconv.ParseFloat(h, 64)
			}
		default:
			if size, err := strconv.ParseFloat(opt, 64); err == nil {
				options.Width = size
				options.Height = size
			}
		}
	}

	return options
}

// Request is an imageproxy request which includes a remote URL of an image to
// proxy, and an optional set of transformations to perform.
type Request struct {
	URL      *url.URL      // URL of the image to proxy
	Options  Options       // Image transformation to perform
	Original *http.Request // The original HTTP request
}

// String returns the request URL as a string, with r.Options encoded in the
// URL fragment.
func (r Request) String() string {
	u := *r.URL
	u.Fragment = r.Options.String()
	return u.String()
}

// NewRequest parses an http.Request into an imageproxy Request.  Options and
// the remote image URL are specified in the request path, formatted as:
// /{options}/{remote_url}.  Options may be omitted, so a request path may
// simply contain /{remote_url}.
//
// The remote URL may be included in plain text without any encoding,
// percent-encoded (aka URL encoded), or base64 encoded (URL safe, no padding).
//
// When no encoding is used, any URL query string is treated as part of the remote URL.
// For example, given the proxy URL of `http://localhost/x/http://example.com/?id=1`,
// the remote URL is `http://example.com/?id=1`.
//
// When percent-encoding is used, the full URL must be encoded.
// Any query string on the proxy URL is NOT included as part of the remote URL.
// Percent-encoded URLs must be absolute URLs;
// they cannot be relative URLs used with a default base URL.
//
// When base64 encoding is used, the full URL must be encoded.
// Any query string on the proxy URL is NOT included as part of the remote URL.
// Base64 encoded URLs may be relative URLs used with a default base URL.
//
// Assuming an imageproxy server running on localhost, the following are all
// valid imageproxy requests:
//
//	http://localhost/100x200/http://example.com/image.jpg
//	http://localhost/100x200,r90/http://example.com/image.jpg?foo=bar
//	http://localhost//http://example.com/image.jpg
//	http://localhost/http://example.com/image.jpg
//	http://localhost/x/http%3A%2F%2Fexample.com%2Fimage.jpg
//	http://localhost/100x200/aHR0cDovL2V4YW1wbGUuY29tL2ltYWdlLmpwZw
func NewRequest(r *http.Request, baseURL *url.URL) (*Request, error) {
	var err error
	req := &Request{Original: r}
	var enc bool // whether the remote URL was base64 or URL encoded

	path := r.URL.EscapedPath()[1:] // strip leading slash
	req.URL, enc, err = parseURL(path, baseURL)
	if err != nil || !req.URL.IsAbs() {
		// first segment should be options
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 {
			return nil, URLError{"too few path segments", r.URL}
		}

		var err error
		req.URL, enc, err = parseURL(parts[1], baseURL)
		if err != nil {
			return nil, URLError{fmt.Sprintf("unable to parse remote URL: %v", err), r.URL}
		}

		req.Options = ParseOptions(parts[0])
	}

	if baseURL != nil {
		req.URL = baseURL.ResolveReference(req.URL)
	}

	if !req.URL.IsAbs() {
		return nil, URLError{"must provide absolute remote URL", r.URL}
	}

	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, URLError{"remote URL must have http or https scheme", r.URL}
	}

	if !enc {
		// if the remote URL was not base64 or URL encoded,
		// then the query string is part of the remote URL
		req.URL.RawQuery = r.URL.RawQuery
	}
	return req, nil
}

var reCleanedURL = regexp.MustCompile(`^(https?):/+([^/])`)
var reIsEncodedURL = regexp.MustCompile(`^(?i)https?%3A%2F`)

// parseURL parses s as a URL, handling URLs that have been munged by
// path.Clean or a webserver that collapses multiple slashes.
// The returned enc bool indicates whether the remote URL was encoded.
func parseURL(s string, baseURL *url.URL) (_ *url.URL, enc bool, _ error) {
	// Try to base64 decode the string. If it is not base64 encoded,
	// this will fail quickly on the first invalid character like ":", ".", or "/".
	// Accept the decoded string if it looks like an absolute HTTP URL,
	// or if we have a baseURL and the decoded string did not contain invalid code points.
	// This allows for values like "/path", which do successfully base64 decode,
	// but not to valid code points, to be treated as an unencoded string.
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		d := string(b)
		if strings.HasPrefix(d, "http://") || strings.HasPrefix(d, "https://") {
			enc = true
			s = d
		} else if baseURL != nil && !strings.ContainsRune(d, unicode.ReplacementChar) {
			enc = true
			s = d
		}
	}

	// If the string looks like a URL encoded absolute HTTP(S) URL, decode it.
	if reIsEncodedURL.MatchString(s) {
		if u, err := url.PathUnescape(s); err == nil {
			enc = true
			s = u
		}
	}

	s = reCleanedURL.ReplaceAllString(s, "$1://$2")
	u, err := url.Parse(s)
	return u, enc, err
}
