package devcontainer

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStringOrSliceUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want StringOrSlice
	}{
		{"string", `"npm install"`, StringOrSlice{"npm install"}},
		{"slice", `["npm","install"]`, StringOrSlice{"npm", "install"}},
		{"null", `null`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s StringOrSlice
			if err := json.Unmarshal([]byte(c.in), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(s, c.want) {
				t.Fatalf("got %#v, want %#v", s, c.want)
			}
		})
	}
}
