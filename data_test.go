// Copyright 2013 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package imageproxy

import (
	"net/http"
	"net/url"
	"testing"
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
			Options{1, 2, true, 90, true, true, 80, "", false, "", 0, 0, 0, 0, false},
			"1x2,fh,fit,fv,q80,r90",
		},
		{
			Options{0.15, 1.3, false, 45, false, false, 95, "c0ffee", false, "png", 0, 0, 0, 0, false},
			"0.15x1.3,png,q95,r45,sc0ffee",
		},
		{
			Options{0.15, 1.3, false, 45, false, false, 95, "c0ffee", false, "", 100, 200, 0, 0, false},
			"0.15x1.3,cx100,cy200,q95,r45,sc0ffee",
		},
		{
			Options{0.15, 1.3, false, 45, false, false, 95, "c0ffee", false, "png", 100, 200, 300, 400, false},
			"0.15x1.3,ch400,cw300,cx100,cy200,png,q95,r45,sc0ffee",
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
		{"q70,1x2,fit,r90,fv,fh,sc0ffee,png", Options{1, 2, true, 90, true, true, 70, "c0ffee", false, "png", 0, 0, 0, 0, false}},
		{"r90,fh,sc0ffee,png,q90,1x2,fv,fit", Options{1, 2, true, 90, true, true, 90, "c0ffee", false, "png", 0, 0, 0, 0, false}},

		// all flags, in different orders with crop
		{"q70,cx100,cw300,1x2,fit,cy200,r90,fv,ch400,fh,sc0ffee,png,sc,scaleUp", Options{1, 2, true, 90, true, true, 70, "c0ffee", true, "png", 100, 200, 300, 400, true}},
		{"ch400,r90,cw300,fh,sc0ffee,png,cx100,q90,cy200,1x2,fv,fit", Options{1, 2, true, 90, true, true, 90, "c0ffee", false, "png", 100, 200, 300, 400, false}},

		// all flags, in different orders with crop & different resizes
		{"q70,cx100,cw300,x2,fit,cy200,r90,fv,ch400,fh,sc0ffee,png", Options{0, 2, true, 90, true, true, 70, "c0ffee", false, "png", 100, 200, 300, 400, false}},
		{"ch400,r90,cw300,fh,sc0ffee,png,cx100,q90,cy200,1x,fv,fit", Options{1, 0, true, 90, true, true, 90, "c0ffee", false, "png", 100, 200, 300, 400, false}},
		{"ch400,r90,cw300,fh,sc0ffee,png,cx100,q90,cy200,cw,fv,fit", Options{0, 0, true, 90, true, true, 90, "c0ffee", false, "png", 100, 200, 0, 400, false}},
		{"ch400,r90,cw300,fh,sc0ffee,png,cx100,q90,cy200,cw,fv,fit,123x321", Options{123, 321, true, 90, true, true, 90, "c0ffee", false, "png", 100, 200, 0, 400, false}},
		{"123x321,ch400,r90,cw300,fh,sc0ffee,png,cx100,q90,cy200,cw,fv,fit", Options{123, 321, true, 90, true, true, 90, "c0ffee", false, "png", 100, 200, 0, 400, false}},
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
		{ // escaped path
			"http://localhost/http://example.com/%2C",
			"http://example.com/%2C", emptyOptions, false,
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
	req, _ := http.NewRequest("GET", "/x/path", nil)
	base, _ := url.Parse("https://example.com/")

	r, err := NewRequest(req, base)
	if err != nil {
		t.Errorf("NewRequest(%v, %v) returned unexpected error: %v", req, base, err)
	}

	want := "https://example.com/path#0x0"
	if got := r.String(); got != want {
		t.Errorf("NewRequest(%v, %v) returned %q, want %q", req, base, got, want)
	}

}
