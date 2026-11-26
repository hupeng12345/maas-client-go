package asr

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		msgType byte
		flags   byte
		seq     int32
		payload []byte
	}{
		{"init", msgTypeFullClient, flagPositiveSeq, 1, []byte(`{"audio":{"format":"pcm"}}`)},
		{"audio", msgTypeAudioOnly, flagPositiveSeq, 2, []byte{1, 2, 3, 4}},
		{"finish empty", msgTypeAudioOnly, flagNegativeSeq, -7, nil},
		{"server result", msgTypeFullServer, flagPositiveSeq, 3, []byte(`{"text":"hi"}`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := encodeASRFrame(tc.msgType, tc.flags, tc.seq, tc.payload)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if raw[0] != frameVersion {
				t.Fatalf("version byte: got %#x", raw[0])
			}
			if raw[1] != tc.msgType<<4|tc.flags {
				t.Fatalf("type/flags byte: got %#x", raw[1])
			}
			if raw[2] != frameSerializationCompression {
				t.Fatalf("serialization/compression byte: got %#x", raw[2])
			}
			if raw[3] != 0 {
				t.Fatalf("reserved byte: got %#x", raw[3])
			}
			if got := int32(binary.BigEndian.Uint32(raw[4:8])); got != tc.seq {
				t.Fatalf("sequence: got %d want %d", got, tc.seq)
			}
			if got := int(binary.BigEndian.Uint32(raw[8:12])); got != len(raw)-asrHeaderLen {
				t.Fatalf("payload length: got %d want %d", got, len(raw)-asrHeaderLen)
			}

			frame, err := decodeASRFrame(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if frame.Type != tc.msgType || frame.Flags != tc.flags || frame.Sequence != tc.seq {
				t.Fatalf("header mismatch: %+v", frame)
			}
			if !bytes.Equal(frame.Payload, tc.payload) {
				t.Fatalf("payload mismatch: got %q want %q", frame.Payload, tc.payload)
			}
		})
	}
}

func TestDecodeErrorFrame(t *testing.T) {
	payload := []byte(`{"parret":{"reason":"unusable_audio"}}`)
	raw := buildRawFrame(t, msgTypeError, flagPositiveSeq, 3, 45000002, payload)

	frame, err := decodeASRFrame(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if frame.Type != msgTypeError {
		t.Fatalf("type: got %d", frame.Type)
	}
	if frame.Code != 45000002 {
		t.Fatalf("code: got %d", frame.Code)
	}
	if string(frame.Payload) != string(payload) {
		t.Fatalf("payload: got %s", frame.Payload)
	}
}

func TestDecodeRejects(t *testing.T) {
	valid, err := encodeASRFrame(msgTypeFullServer, flagPositiveSeq, 1, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	badVersion := append([]byte(nil), valid...)
	badVersion[0] = 0x21

	badLength := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(badLength[8:12], 1<<20)

	cases := map[string][]byte{
		"empty":       {},
		"too short":   {0x11, 0x91},
		"bad version": badVersion,
		"bad length":  badLength,
		"error short": {0x11, 0xF1, 0x11, 0x00, 0, 0, 0, 1, 0, 0},
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeASRFrame(raw); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

// buildRawFrame assembles a 16-byte-header error frame for tests.
func buildRawFrame(t *testing.T, msgType, flags byte, seq int32, code uint32, payload []byte) []byte {
	t.Helper()
	zraw, err := encodeASRFrame(msgType, flags, seq, payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out := make([]byte, len(zraw)+4)
	copy(out[:8], zraw[:8])
	binary.BigEndian.PutUint32(out[8:12], code)
	copy(out[12:16], zraw[8:12])
	copy(out[16:], zraw[12:])
	return out
}
