package parser

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		tokens  [][]byte
		want    Command
		wantErr bool
	}{
		{
			name:   "set with args",
			tokens: [][]byte{[]byte("SET"), []byte("k"), []byte("v")},
			want:   Command{Name: "set", Args: [][]byte{[]byte("k"), []byte("v")}},
		},
		{
			name:   "case insensitive",
			tokens: [][]byte{[]byte("GeT"), []byte("k")},
			want:   Command{Name: "get", Args: [][]byte{[]byte("k")}},
		},
		{
			name:   "no args",
			tokens: [][]byte{[]byte("PING")},
			want:   Command{Name: "ping", Args: nil},
		},
		{
			name:    "empty tokens",
			tokens:  nil,
			wantErr: true,
		},
		{
			name:    "empty name",
			tokens:  [][]byte{[]byte("")},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.tokens)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Name != tt.want.Name || !reflect.DeepEqual(got.Args, tt.want.Args) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
