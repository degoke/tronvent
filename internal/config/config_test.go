package config

import "testing"

func TestValidateTronGridAPIKeys(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "single key", raw: "key-a", want: true},
		{name: "multiple keys", raw: "key-a,key-b", want: true},
		{name: "whitespace around key", raw: " , key-a , ", want: true},
		{name: "empty", raw: "", want: false},
		{name: "only separators", raw: ",,", want: false},
		{name: "only whitespace", raw: " ,  , ", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTronGridAPIKeys(tt.raw)
			if (err == nil) != tt.want {
				t.Fatalf("validateTronGridAPIKeys(%q) error = %v, want valid = %t", tt.raw, err, tt.want)
			}
		})
	}
}
