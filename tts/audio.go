package tts

import (
	"encoding/binary"
	maas "github.com/hupeng12345/maas-client-go"
	"io"
	"os"
)

// Audio is a streaming audio response.
//
// The platform returns raw signed 16-bit little-endian PCM without a container.
// Always read the sample rate from this struct (it comes from the response
// header); the requested value is not guaranteed to be honoured.
//
// Audio is not safe for concurrent use, and the caller must Close it.
type Audio struct {
	rc io.ReadCloser

	// SampleRate is the effective sample rate in Hz (X-Sample-Rate).
	SampleRate int
	// Channels is the channel count (X-Audio-Channels); always 1 today.
	Channels int
	// Format is the audio encoding (X-Audio-Format), e.g. "pcm_s16le".
	Format string
	// ContentType is the response media type.
	ContentType string
	// Meta carries the request ID and latency.
	Meta maas.ResponseMeta
}

// Read implements io.Reader.
func (a *Audio) Read(p []byte) (int, error) { return a.rc.Read(p) }

// Close releases the connection.
func (a *Audio) Close() error { return a.rc.Close() }

// WriteWAV writes the remaining audio as a WAV file body to w.
//
// It streams as it copies: memory usage stays constant regardless of length.
func (a *Audio) WriteWAV(w io.Writer) (int64, error) {
	if err := writeWAVHeader(w, a.SampleRate, a.Channels, 0); err != nil {
		return 0, err
	}
	n, err := io.Copy(w, a)
	return n + wavHeaderSize, err
}

// WriteWAVFile writes the remaining audio to a WAV file at path.
func (a *Audio) WriteWAVFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := a.WriteWAV(f); err != nil {
		return err
	}
	return f.Close()
}

// Bytes reads the remaining audio into memory.
//
// Prefer streaming for long outputs; this helper is for short clips.
func (a *Audio) Bytes() ([]byte, error) { return io.ReadAll(a) }

// wavHeaderSize is the size of a canonical PCM WAV header.
const wavHeaderSize int64 = 44

// writeWAVHeader writes a 44-byte canonical PCM WAV header.
//
// dataSize 0 means "unknown", which is valid for streaming playback but some
// strict parsers expect the real size; use WriteWAVFile for files.
func writeWAVHeader(w io.Writer, sampleRate, channels int, dataSize uint32) error {
	if sampleRate <= 0 {
		sampleRate = 8000
	}
	if channels <= 0 {
		channels = 1
	}
	const bitsPerSample = 16
	byteRate := uint32(sampleRate * channels * bitsPerSample / 8)
	blockAlign := uint16(channels * bitsPerSample / 8)

	var h [44]byte
	copy(h[0:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:8], uint32(36)+dataSize)
	copy(h[8:12], "WAVE")
	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:20], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(h[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(h[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(h[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(h[28:32], byteRate)
	binary.LittleEndian.PutUint16(h[32:34], blockAlign)
	binary.LittleEndian.PutUint16(h[34:36], bitsPerSample)
	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:44], dataSize)

	_, err := w.Write(h[:])
	return err
}
