// Copyright 2013 The imageproxy authors.
// SPDX-License-Identifier: Apache-2.0

package imageproxy

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

var emptyOptions = Options{}

func TestOptions_String(t *testing.T) {
	tests := []struct {
		Options Options
		String  string
	}{
		{
			emptyOptions,
			"0x0",
		},
		{
			Options{Width: 1, Height: 2, Fit: true, Rotate: 90, FlipVertical: true, FlipHorizontal: true, Quality: 80},
			"1x2,fh,fit,fv,q80,r90",
		},
		{
			Options{Width: 0.15, Height: 1.3, Rotate: 45, Quality: 95, Signature: "c0ffee", Format: "png", ValidUntil: time.Unix(123, 0)},
			"0.15x1.3,png,q95,r45,sc0ffee,vu123",
		},
		{
			Options{Width: 0.15, Height: 1.3, CropX: 100, CropY: 200},
			"0.15x1.3,cx100,cy200",
		},
		{
			Options{ScaleUp: true, CropX: 100, CropY: 200, CropWidth: 300, CropHeight: 400, SmartCrop: true},
			"0x0,ch400,cw300,cx100,cy200,sc,scaleUp",
		},
	}

	for i, tt := range tests {
		if got, want := tt.Options.String(), tt.String; got != want {
			t.Errorf("%d. Options.String returned %v, want %v", i, got, want)
		}
	}
}

func TestParseOptions(t *testing.T) {
	tests := []struct {
		Input   string
		Options Options
	}{
		{"", emptyOptions},
		{"x", emptyOptions},
		{"r", emptyOptions},
		{"0", emptyOptions},
		{",,,,", emptyOptions},

		// size variations
		{"1x", Options{Width: 1}},
		{"x1", Options{Height: 1}},
		{"1x2", Options{Width: 1, Height: 2}},
		{"-1x-2", Options{Width: -1, Height: -2}},
		{"0.1x0.2", Options{Width: 0.1, Height: 0.2}},
		{"1", Options{Width: 1, Height: 1}},
		{"0.1", Options{Width: 0.1, Height: 0.1}},

		// additional flags
		{"fit", Options{Fit: true}},
		{"r90", Options{Rotate: 90}},
		{"fv", Options{FlipVertical: true}},
		{"fh", Options{FlipHorizontal: true}},
		{"jpeg", Options{Format: "jpeg"}},

		// duplicate flags (last one wins)
		{"1x2,3x4", Options{Width: 3, Height: 4}},
		{"1x2,3", Options{Width: 3, Height: 3}},
		{"1x2,0x3", Options{Width: 0, Height: 3}},
		{"1x,x2", Options{Width: 1, Height: 2}},
		{"r90,r270", Options{Rotate: 270}},
		{"jpeg,png", Options{Format: "png"}},

		// mix of valid and invalid flags
		{"FOO,1,BAR,r90,BAZ", Options{Width: 1, Height: 1, Rotate: 90}},

		// flags, in different orders
		{"q70,1x2,fit,r90,fv,fh,sc0ffee,png", Options{Width: 1, Height: 2, Fit: true, Rotate: 90, FlipVertical: true, FlipHorizontal: true, Quality: 70, Signature: "c0ffee", Format: "png"}},
		{"r90,fh,sc0ffee,png,q90,1x2,fv,fit", Options{Width: 1, Height: 2, Fit: true, Rotate: 90, FlipVertical: true, FlipHorizontal: true, Quality: 90, Signature: "c0ffee", Format: "png"}},
		{"cx100,cw300,1x2,cy200,ch400,sc,scaleUp,vu1234567890", Options{Width: 1, Height: 2, ScaleUp: true, CropX: 100, CropY: 200, CropWidth: 300, CropHeight: 400, SmartCrop: true, ValidUntil: time.Unix(1234567890, 0)}},
	}

	for _, tt := range tests {
		if got, want := ParseOptions(tt.Input), tt.Options; got != want {
			t.Errorf("ParseOptions(%q) returned %#v, want %#v", tt.Input, got, want)
		}
	}
}

// Test that request URLs are properly parsed into Options and RemoteURL.  This
// test verifies that invalid remote URLs throw errors, and that valid
// combinations of Options and URL are accept.  This does not exhaustively test
// the various Options that can be specified; see TestParseOptions for that.
func TestNewRequest(t *testing.T) {
	tests := []struct {
		URL         string  // input URL to parse as an imageproxy request
		RemoteURL   string  // expected URL of remote image parsed from input
		Options     Options // expected options parsed from input
		ExpectError bool    // whether an error is expected from NewRequest
	}{
		// invalid URLs
		{"http://localhost/", "", emptyOptions, true},
		{"http://localhost/1/", "", emptyOptions, true},
		{"http://localhost//example.com/foo", "", emptyOptions, true},
		{"http://localhost//ftp://example.com/foo", "", emptyOptions, true},

		// invalid options.  These won't return errors, but will not fully parse the options
		{
			"http://localhost/s/http://example.com/",
			"http://example.com/", emptyOptions, false,
		},
		{
			"http://localhost/1xs/http://example.com/",
			"http://example.com/", Options{Width: 1}, false,
		},

		// valid URLs
		{
			"http://localhost/http://example.com/foo",
			"http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost//http://example.com/foo",
			"http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost//https://example.com/foo",
			"https://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/1x2/http://example.com/foo",
			"http://example.com/foo", Options{Width: 1, Height: 2}, false,
		},
		{
			"http://localhost//http://example.com/foo?bar",
			"http://example.com/foo?bar", emptyOptions, false,
		},
		{
			"http://localhost/http:/example.com/foo",
			"http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/http:///example.com/foo",
			"http://example.com/foo", emptyOptions, false,
		},
		// base64 encoded paths
		{
			"http://localhost/aHR0cDovL2V4YW1wbGUuY29tL2Zvbw",
			"http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost//aHR0cDovL2V4YW1wbGUuY29tL2Zvbw",
			"http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/x/aHR0cDovL2V4YW1wbGUuY29tL2Zvbw",
			"http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/x/aHR0cHM6Ly9leGFtcGxlLmNvbS9mb28_YmFy",
			"https://example.com/foo?bar", emptyOptions, false,
		},
		{
			"http://localhost/x/aHR0cHM6Ly9leGFtcGxlLmNvbS9mb28_YmFy?baz",
			"https://example.com/foo?bar", emptyOptions, false,
		},
		{ // escaped path
			"http://localhost/http://example.com/%2C",
			"http://example.com/%2C", emptyOptions, false,
		},
		// percent encoded cases
		{
			"http://localhost/1x2/http%3A%2F%2Fexample.com%2Ffoo",
			"http://example.com/foo", Options{Width: 1, Height: 2}, false,
		},
		{
			"http://localhost/1x2/http%3A%2F%2Fexample.com%2Fhttp%2Fstuff",
			"http://example.com/http/stuff", Options{Width: 1, Height: 2}, false,
		},
		{
			"http://localhost/http%3A%2F%2Fexample.com%2Ffoo",
			"http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/HTTP%3a%2f%2fexample.com%2Ffoo",
			"http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/http%3A%2Fexample.com%2Ffoo",
			"http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/http%3A%2F%2F%2Fexample.com%2Ffoo",
			"http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost//http%3A%2F%2Fexample.com%2Ffoo",
			"http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/http%3A%2F%2Fexample.com%2Ffoo%3Ftest%3D1%26test%3D2",
			"http://example.com/foo?test=1&test=2", emptyOptions, false,
		},
		{
			"http://localhost/1x2/http%3A%2F%2Fexample.com%2Ffoo%3Ftest%3D1%26test%3D2",
			"http://example.com/foo?test=1&test=2", Options{Width: 1, Height: 2}, false,
		},
	}

	for _, tt := range tests {
		req, err := http.NewRequest("GET", tt.URL, nil)
		if err != nil {
			t.Errorf("http.NewRequest(%q) returned error: %v", tt.URL, err)
			continue
		}

		r, err := NewRequest(req, nil)
		if tt.ExpectError {
			if err == nil {
				t.Errorf("NewRequest(%v) did not return expected error", req)
			}
			continue
		} else if err != nil {
			t.Errorf("NewRequest(%v) return unexpected error: %v", req, err)
			continue
		}

		if got, want := r.URL.String(), tt.RemoteURL; got != want {
			t.Errorf("NewRequest(%q) request URL = %v, want %v", tt.URL, got, want)
		}
		if got, want := r.Options, tt.Options; got != want {
			t.Errorf("NewRequest(%q) request options = %v, want %v", tt.URL, got, want)
		}
	}
}

func TestNewRequest_BaseURL(t *testing.T) {
	base, _ := url.Parse("https://example.com/")

	tests := []struct {
		path string
		want string
	}{
		{
			path: "/x/path",
			want: "https://example.com/path#0x0",
		},
		{ // Chinese characters 已然
			path: "/x/5bey54S2",
			want: "https://example.com/%E5%B7%B2%E7%84%B6#0x0",
		},
	}

	for _, tt := range tests {
		req, _ := http.NewRequest("GET", tt.path, nil)
		r, err := NewRequest(req, base)
		if err != nil {
			t.Errorf("NewRequest(%v, %v) returned unexpected error: %v", req, base, err)
		}

		if got := r.String(); got != tt.want {
			t.Errorf("NewRequest(%v, %v) returned %q, want %q", req, base, got, tt.want)
		}
	}
}
