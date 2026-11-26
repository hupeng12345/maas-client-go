package asr

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frame protocol constants for the bidirectional streaming ASR protocol.
//
// Every message is a WebSocket binary message whose payload is a single gzip
// member. The header carries the message type, a signed sequence number and the
// compressed payload length.
const (
	// frameVersion masks the protocol version and header length in byte 0.
	frameVersion = 0x11
	// frameSerializationCompression is byte 2: JSON serialisation plus gzip.
	frameSerializationCompression = 0x11

	// msgTypeFullClient is a complete client request (session configuration).
	msgTypeFullClient = 0x1
	// msgTypeAudioOnly is an audio-only client message.
	msgTypeAudioOnly = 0x2
	// msgTypeFullServer is a complete server response.
	msgTypeFullServer = 0x9
	// msgTypeError is a server error response.
	msgTypeError = 0xF

	// flagPositiveSeq marks a message with a positive sequence number.
	flagPositiveSeq = 0x1
	// flagLastNoSeq marks the final message.
	flagLastNoSeq = 0x2
	// flagNegativeSeq marks the final message carrying a negative sequence.
	flagNegativeSeq = 0x3
)

// asrHeaderLen is the size of a normal frame header.
const asrHeaderLen = 12

// asrErrorHeaderLen is the size of an error frame header: it carries a 4-byte
// error code between the sequence number and the payload length.
const asrErrorHeaderLen = 16

// gzipEmptyMemberLen is the length of a gzip member with an empty payload.
const gzipEmptyMemberLen = 20

// asrFrame is a decoded protocol frame.
type asrFrame struct {
	// Type is the message type nibble.
	Type byte
	// Flags is the flag nibble.
	Flags byte
	// Sequence is the signed sequence number.
	Sequence int32
	// Code is the error code, only set for error frames.
	Code uint32
	// Payload is the decompressed payload.
	Payload []byte
}

// encodeASRFrame serialises one frame, gzipping the payload.
//
// An empty payload is still compressed into a valid empty gzip member, which is
// what the protocol expects for a finish frame without trailing audio.
func encodeASRFrame(msgType, flags byte, seq int32, payload []byte) ([]byte, error) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if len(payload) > 0 {
		if _, err := zw.Write(payload); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}

	body := compressed.Bytes()
	out := make([]byte, asrHeaderLen+len(body))
	out[0] = frameVersion
	out[1] = msgType<<4 | flags
	out[2] = frameSerializationCompression
	out[3] = 0
	binary.BigEndian.PutUint32(out[4:8], uint32(seq))
	binary.BigEndian.PutUint32(out[8:12], uint32(len(body)))
	copy(out[asrHeaderLen:], body)
	return out, nil
}

// decodeASRFrame parses a server frame.
//
// The header length depends on the message type: error frames carry an extra
// 4-byte code, so the parser must branch on the high nibble of byte 1 before
// reading the payload length. Getting this wrong shows up as a gzip failure.
func decodeASRFrame(b []byte) (*asrFrame, error) {
	if len(b) < asrHeaderLen {
		return nil, fmt.Errorf("asr: frame too short: %d bytes", len(b))
	}
	if b[0]&0xF0 != frameVersion&0xF0 {
		return nil, fmt.Errorf("asr: unsupported protocol version 0x%02x", b[0])
	}

	msgType := b[1] >> 4
	flags := b[1] & 0x0F

	f := &asrFrame{
		Type:     msgType,
		Flags:    flags,
		Sequence: int32(binary.BigEndian.Uint32(b[4:8])),
	}

	offset := asrHeaderLen
	if msgType == msgTypeError {
		if len(b) < asrErrorHeaderLen {
			return nil, fmt.Errorf("asr: error frame too short: %d bytes", len(b))
		}
		f.Code = binary.BigEndian.Uint32(b[8:12])
		offset = asrErrorHeaderLen
	}

	size := int(binary.BigEndian.Uint32(b[offset-4 : offset]))
	if size < 0 || offset+size > len(b) {
		return nil, fmt.Errorf("asr: payload size %d exceeds frame length %d", size, len(b))
	}

	compressed := b[offset : offset+size]
	if len(compressed) == 0 {
		return f, nil
	}

	// Byte 2 low nibble 1 means gzip; 0 means the payload is already plain.
	if b[2]&0x0F == 1 {
		zr, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return nil, fmt.Errorf("asr: open gzip payload: %w", err)
		}
		plain, err := io.ReadAll(zr)
		closeErr := zr.Close()
		if err != nil {
			return nil, fmt.Errorf("asr: read gzip payload: %w", err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("asr: close gzip payload: %w", closeErr)
		}
		f.Payload = plain
		return f, nil
	}

	f.Payload = append([]byte(nil), compressed...)
	return f, nil
}

// errASRFrame is returned when a frame is structurally invalid.
var errASRFrame = errors.New("asr: invalid frame")
