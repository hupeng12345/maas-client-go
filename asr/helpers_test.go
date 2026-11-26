package asr

import (
	"testing"
)

func TestIsUUID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"41169504-2acb-46d4-a59d-f4c1b1572240", true},
		{"00000000-0000-4000-8000-000000000000", true},
		{"ABCDEF01-2345-6789-ABCD-EF0123456789", true},
		{"", false},
		{"abc", false},
		{"411695042acb46d4a59df4c1b1572240", false},
		{"41169504-2acb-46d4-a59d-f4c1b157224", false},
		{"41169504-2acb-46d4-a59d-f4c1b15722400", false},
		{"41169504-2acb-46d4-a59d-f4c1b157224g", false},
	}
	for _, tc := range cases {
		if got := isUUID(tc.in); got != tc.want {
			t.Errorf("isUUID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestClipPayload(t *testing.T) {
	cases := []struct {
		in   []byte
		n    int
		want int
	}{
		{nil, 4, 0},
		{[]byte("abc"), 4, 3},
		{[]byte("abcdef"), 4, 4},
		{[]byte("abcdef"), 0, 0},
	}
	for _, tc := range cases {
		got := clipPayload(tc.in, tc.n)
		if len(got) != tc.want {
			t.Errorf("clipPayload(len=%d,n=%d) len = %d, want %d", len(tc.in), tc.n, len(got), tc.want)
		}
	}
	// The result must be a copy, not an alias of the input.
	src := []byte("abcdef")
	out := clipPayload(src, 3)
	out[0] = 'z'
	if src[0] == 'z' {
		t.Error("clipPayload must copy")
	}
}

func TestEventKindString(t *testing.T) {
	cases := []struct {
		kind EventKind
		want string
	}{
		{EventReady, "ready"},
		{EventSnapshot, "snapshot"},
		{EventUtteranceSettled, "utterance"},
		{EventTerminal, "terminal"},
		{EventError, "error"},
		{EventKind(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("EventKind(%d).String() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestErrorHint(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{"unusable audio", `{"parret":{"reason":"unusable_audio"}}`, "unusable_audio: audio was unusable"},
		{"invalid sequence", `{"parret":{"reason":"invalid_sequence"}}`, "invalid_sequence:"},
		{"capacity", `{"parret":{"reason":"capacity_exhausted"}}`, "capacity_exhausted:"},
		{"hotword table", `{"code":"403003"}`, "hotword table is not authorised"},
		{"unknown reason", `{"parret":{"reason":"brand_new"}}`, "brand_new:"},
		{"no reason", `{}`, "inspect the error frame payload"},
		{"bad json", `not json`, "inspect the error frame payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := asrErrorHint([]byte(tc.payload))
			if !contains(got, tc.want) {
				t.Fatalf("errorHint = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestIsSuccess(t *testing.T) {
	cases := map[string]bool{
		"succeeded": true,
		"degraded":  false,
		"failed":    false,
		"cancelled": false,
		"":          false,
	}
	for status, want := range cases {
		if got := isASRSuccess(status); got != want {
			t.Errorf("isASRSuccess(%q) = %v, want %v", status, got, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
