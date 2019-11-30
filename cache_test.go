// Copyright 2013 Google LLC. All rights reserved.
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

import "testing"

func TestNopCache(t *testing.T) {
	data, ok := NopCache.Get("foo")
	if data != nil {
		t.Errorf("NopCache.Get returned non-nil data")
	}
	if ok != false {
		t.Errorf("NopCache.Get returned ok = true, should always be false.")
	}

	// nothing to test on these methods other than to verify they exist
	NopCache.Set("", []byte{})
	NopCache.Delete("")
}
