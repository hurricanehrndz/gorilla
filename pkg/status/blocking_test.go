package status

import (
	"reflect"
	"testing"
)

// TestMatchBlockingApps verifies the case-insensitive, ".exe"-optional matching
// contract on both sides (spec R6): "Notepad", "notepad.exe", and "NOTEPAD.EXE"
// all match a running "notepad.exe".
func TestMatchBlockingApps(t *testing.T) {
	running := []string{"notepad.exe", "explorer.exe"}

	cases := []struct {
		name string
		apps []string
		want []string
	}{
		{"bare name", []string{"Notepad"}, []string{"Notepad"}},
		{"with .exe", []string{"notepad.exe"}, []string{"notepad.exe"}},
		{"upper .exe", []string{"NOTEPAD.EXE"}, []string{"NOTEPAD.EXE"}},
		{"not running", []string{"firefox"}, nil},
		{"mixed", []string{"firefox", "Notepad"}, []string{"Notepad"}},
		{"empty", nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchBlockingApps(tc.apps, running)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("matchBlockingApps(%v) = %v, want %v", tc.apps, got, tc.want)
			}
		})
	}
}
