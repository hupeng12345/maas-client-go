package asr

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	maas "github.com/hupeng12345/maas-client-go"
)

// EventKind classifies a session event.
type EventKind int

// Session event kinds.
const (
	// EventReady means the configuration was accepted; audio may be sent.
	EventReady EventKind = iota
	// EventSnapshot is an intermediate transcript that may still change.
	EventSnapshot
	// EventUtteranceSettled marks a settled sentence.
	EventUtteranceSettled
	// EventTerminal is the authoritative end of the session.
	EventTerminal
	// EventError reports a failure; the session is finished.
	EventError
)

// String returns a readable event name.
func (k EventKind) String() string {
	switch k {
	case EventReady:
		return "ready"
	case EventSnapshot:
		return "snapshot"
	case EventUtteranceSettled:
		return "utterance"
	case EventTerminal:
		return "terminal"
	case EventError:
		return "error"
	default:
		return "unknown"
	}
}

// Utterance is one recognised sentence.
type Utterance struct {
	Text     string
	Definite bool
	StartMS  int
	EndMS    int
}

// Event is emitted by a session.
type Event struct {
	Kind EventKind
	// Text is always the latest full transcription, never an increment.
	Text       string
	Utterances []Utterance
	// IsFinal marks the last transcript frame of the session.
	IsFinal bool
	// Status is the terminal status, for example "succeeded".
	Status string
	// LogID is the platform log identifier, useful for support.
	LogID    string
	Duration time.Duration
	Err      error
	Raw      json.RawMessage
}

// Result is the aggregated outcome of a session.
type Result struct {
	Meta maas.ResponseMeta
	// Status is the terminal status reported by the server.
	Status string
	// Text is the final transcript.
	Text string
	// Utterances holds settled sentences, when requested.
	Utterances []Utterance
	// Duration is the audio duration reported by the server.
	Duration time.Duration
	// LogID is the platform log identifier.
	LogID string
	// Succeeded reports whether the session ended successfully.
	Succeeded bool
}

// Session is an open ASR stream.
//
// Lifecycle:
//
//	sess, err := client.ASR.Start(ctx, cfg)  // returns once the server is ready
//	for ev := range sess.Events() { ... }     // must be consumed, or writes block
//	sess.Write(pcm)                           // blocks for backpressure
//	res, err := sess.Finish(ctx)              // sends the end frame, waits for the terminal state
//
// A Session is not safe for concurrent Write calls, but Events may be consumed
// from another goroutine.
type Session struct {
	conn      *websocket.Conn
	events    chan Event
	ready     chan struct{}
	readyOnce sync.Once
	readyDone atomic.Bool
	fail      chan error
	done      chan struct{}
	writeMu   sync.Mutex
	seq       int32
	chunkSize int
	closed    atomic.Bool
	closeOnce sync.Once

	mu     sync.Mutex
	result *Result
	runErr error
	meta   maas.ResponseMeta
	url    string
}

// Start opens a session and blocks until the server is ready for audio.
//
// Handshake failures, a rejected configuration and timeouts all surface here,
// so a nil error means it is safe to call Write immediately.
func (s *Service) Start(ctx context.Context, cfg Config) (*Session, error) {
	const op = "asr.Start"

	if ctx == nil {
		ctx = context.Background()
	}

	endpoint, err := s.endpointURL(cfg)
	if err != nil {
		return nil, err
	}

	payload, err := cfg.initPayload()
	if err != nil {
		return nil, &maas.Error{Kind: maas.KindLocal, Message: "cannot build session configuration", Hint: "check Config fields", Op: op, Err: err}
	}

	ctx, apiRequestID, requestID := sessionRequestIDs(ctx)
	headers, err := s.handshakeHeaders(cfg, apiRequestID, requestID)
	if err != nil {
		return nil, err
	}

	dialer := &websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		// The protocol does not negotiate a subprotocol; sending one makes some
		// deployments close the connection silently after the handshake.
		Subprotocols: nil,
	}
	conn, resp, err := dialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		return nil, handshakeError(op, resp, err)
	}

	meta := maas.ResponseMeta{
		RequestID:  requestID,
		HTTPStatus: http.StatusSwitchingProtocols,
	}
	if resp != nil {
		meta.HTTPStatus = resp.StatusCode
	}

	sess := &Session{
		conn:      conn,
		events:    make(chan Event, 64),
		ready:     make(chan struct{}),
		fail:      make(chan error, 1),
		done:      make(chan struct{}),
		chunkSize: cfg.chunkSamples(),
		meta:      meta,
		url:       endpoint,
	}

	initFrame, err := encodeASRFrame(msgTypeFullClient, flagPositiveSeq, 1, payload)
	if err != nil {
		_ = conn.Close()
		return nil, &maas.Error{Kind: maas.KindProtocol, Message: "cannot encode session configuration", Op: op, Err: err}
	}
	sess.seq = 1
	if err := conn.WriteMessage(websocket.BinaryMessage, initFrame); err != nil {
		_ = conn.Close()
		return nil, &maas.Error{Kind: maas.KindTransport, Message: "failed to send session configuration", Hint: "check connectivity and handshake headers", Op: op, Err: err}
	}

	go sess.readLoop()

	timeout := cfg.ReadyTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-sess.ready:
		return sess, nil
	case err := <-sess.fail:
		_ = sess.Close()
		return nil, err
	case <-timer.C:
		_ = sess.Close()
		return nil, &maas.Error{
			Kind:      maas.KindTransport,
			Message:   "timed out waiting for the session to become ready",
			Hint:      "the handshake succeeded but no ready frame arrived; check the configuration and any Sec-WebSocket-Protocol header",
			RequestID: requestID,
			Op:        op,
		}
	case <-ctx.Done():
		_ = sess.Close()
		return nil, &maas.Error{Kind: maas.KindTransport, Message: "canceled while opening the session", Op: op, Err: ctx.Err(), RequestID: requestID}
	}
}

// handshakeHeaders builds the handshake headers for the configured auth form.
func (s *Service) handshakeHeaders(cfg Config, apiRequestID, requestID string) (http.Header, error) {
	h := http.Header{}
	h.Set(maas.HeaderAPIRequestID, apiRequestID)
	h.Set(maas.HeaderRequestID, requestID)
	h.Set("User-Agent", s.c.Config().UserAgent)

	auth := cfg.Auth
	switch {
	case strings.TrimSpace(auth.ResourceID) != "":
		key := auth.APIKey
		if key == "" {
			key = s.c.Config().APIKey
		}
		if key == "" {
			return nil, &maas.Error{Kind: maas.KindConfig, Message: "missing API key for the direct ASR auth form", Hint: "set Auth.APIKey or WithAPIKey"}
		}
		h.Set("X-Api-Key", key)
		h.Set("X-Api-Resource-Id", auth.ResourceID)
	case strings.TrimSpace(auth.AppKey) != "" && strings.TrimSpace(auth.AccessKey) != "":
		h.Set("X-Api-App-Key", auth.AppKey)
		h.Set("X-Api-Access-Key", auth.AccessKey)
	default:
		key := auth.BearerKey
		if key == "" {
			key = s.c.Config().APIKey
		}
		if key == "" {
			return nil, &maas.Error{Kind: maas.KindConfig, Message: "missing API key", Hint: "pass WithAPIKey when building the client"}
		}
		h.Set("Authorization", "Bearer "+key)
	}
	for k, v := range cfg.Headers {
		if v == "" {
			continue
		}
		if h.Get(k) != "" {
			continue
		}
		h.Set(k, v)
	}
	return h, nil
}

// handshakeError converts a dial failure into *maas.Error.
func handshakeError(op string, resp *http.Response, err error) error {
	e := &maas.Error{Kind: maas.KindTransport, Message: "ASR handshake failed", Hint: "check the URL, credentials and network", Op: op, Err: err}
	if resp != nil {
		e.HTTPStatus = resp.StatusCode
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			e.Kind = maas.KindAPI
			e.Message = "unauthorized"
			e.Hint = "the API key is missing, wrong or expired"
		case http.StatusNotFound:
			e.Kind = maas.KindAPI
			e.Message = "endpoint not found"
			e.Hint = "the ASR path is wrong; check Config.URL"
		case http.StatusBadRequest:
			e.Kind = maas.KindAPI
			e.Message = "bad handshake request"
			e.Hint = "use a GET upgrade and remove duplicate headers"
		case http.StatusTooManyRequests:
			e.Kind = maas.KindAPI
			e.Retryable = true
			e.Message = "rate limited"
			e.Hint = "retry later"
		}
	}
	return e
}

// markReady closes the ready channel exactly once.
func (s *Session) markReady() {
	s.readyOnce.Do(func() {
		s.readyDone.Store(true)
		close(s.ready)
	})
}

// signalFail reports a pre-ready failure to Start.
func (s *Session) signalFail(err error) {
	select {
	case s.fail <- err:
	default:
	}
}

// Events returns the event stream. It is closed when the session ends.
//
// The stream must be consumed; otherwise the read loop blocks once the buffer
// fills and Finish cannot complete.
func (s *Session) Events() <-chan Event { return s.events }

// Write sends one chunk of audio.
//
// The chunk is gzipped and framed by the SDK; sequence numbers are managed for
// you. It blocks while the socket is busy, providing natural backpressure.
func (s *Session) Write(pcm []byte) (int, error) {
	if len(pcm) == 0 {
		return 0, nil
	}
	if s.closed.Load() {
		return 0, &maas.Error{Kind: maas.KindLocal, Message: "session is closed", Hint: "open a new session", Op: "asr.Write"}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.seq++
	frame, err := encodeASRFrame(msgTypeAudioOnly, flagPositiveSeq, s.seq, pcm)
	if err != nil {
		return 0, &maas.Error{Kind: maas.KindProtocol, Message: "cannot encode audio frame", Op: "asr.Write", Err: err}
	}
	if err := s.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return 0, &maas.Error{Kind: maas.KindTransport, Message: "failed to send audio", Hint: "the connection may have been closed", Op: "asr.Write", Err: err, RequestID: s.meta.RequestID}
	}
	return len(pcm), nil
}

// ChunkSize returns the recommended chunk size in samples.
func (s *Session) ChunkSize() int { return s.chunkSize }

// RequestID returns the request ID used for this session.
func (s *Session) RequestID() string { return s.meta.RequestID }

// Finish sends the end frame and waits for the terminal state.
//
// The returned result is populated even when the session failed, so partial
// transcripts are preserved.
func (s *Session) Finish(ctx context.Context) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.closed.Load() {
		return s.resultOrError()
	}

	s.writeMu.Lock()
	next := s.seq + 1
	s.seq = next
	finishFrame, err := encodeASRFrame(msgTypeAudioOnly, flagNegativeSeq, -next, nil)
	if err != nil {
		s.writeMu.Unlock()
		return nil, &maas.Error{Kind: maas.KindProtocol, Message: "cannot encode finish frame", Op: "asr.Finish", Err: err}
	}
	writeErr := s.conn.WriteMessage(websocket.BinaryMessage, finishFrame)
	s.writeMu.Unlock()

	if writeErr != nil {
		s.setError(&maas.Error{Kind: maas.KindTransport, Message: "failed to send the finish frame", Hint: "the connection may have been dropped; keep the partial transcript", Op: "asr.Finish", Err: writeErr})
		return s.resultOrError()
	}

	select {
	case <-s.done:
		return s.resultOrError()
	case <-ctx.Done():
		// The terminal frame never arrived: the outcome is unknown, so never
		// report success here even if partial text exists.
		res, err := s.resultOrError()
		if err != nil {
			return res, err
		}
		return res, &maas.Error{
			Kind:      maas.KindTransport,
			Message:   "timed out waiting for the terminal state",
			Hint:      "keep the partial transcript and treat the session as incomplete",
			RequestID: s.meta.RequestID,
			Op:        "asr.Finish",
			Retryable: true,
		}
	}
}

// Close releases the connection. It is safe to call multiple times.
func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		err = s.conn.Close()
	})
	return err
}

// resultOrError returns the aggregated result, or the stored error.
func (s *Session) resultOrError() (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runErr != nil {
		if s.result != nil {
			return s.result, s.runErr
		}
		return nil, s.runErr
	}
	return s.result, nil
}

// setError records the first failure.
func (s *Session) setError(err error) {
	s.mu.Lock()
	if s.runErr == nil {
		s.runErr = err
	}
	s.mu.Unlock()
}

// emit delivers an event, respecting closure.
func (s *Session) emit(ev Event) {
	s.mu.Lock()
	if ev.Kind == EventTerminal || ev.Kind == EventError || ev.Text != "" || ev.Kind == EventReady {
		if s.result == nil {
			s.result = &Result{Meta: s.meta}
		}
		if ev.Text != "" {
			s.result.Text = ev.Text
		}
		if len(ev.Utterances) > 0 {
			s.result.Utterances = ev.Utterances
		}
		if ev.LogID != "" {
			s.result.LogID = ev.LogID
		}
		if ev.Duration > 0 {
			s.result.Duration = ev.Duration
		}
		if ev.Kind == EventTerminal {
			s.result.Status = ev.Status
			s.result.Succeeded = isASRSuccess(ev.Status)
		}
	}
	s.mu.Unlock()

	select {
	case s.events <- ev:
	case <-s.done:
	}
}

// readLoop consumes server frames until the session ends.
func (s *Session) readLoop() {
	defer close(s.done)
	defer close(s.events)
	defer s.closed.Store(true)

	for {
		msgType, data, err := s.conn.ReadMessage()
		if err != nil {
			if s.closed.Load() {
				return
			}
			e := &maas.Error{Kind: maas.KindTransport, Message: "ASR connection closed", Hint: "keep the partial transcript; the terminal state is unknown", Op: "asr.read", Err: err, RequestID: s.meta.RequestID}
			s.setError(e)
			s.signalFail(e)
			return
		}
		if msgType != websocket.BinaryMessage {
			continue
		}

		frame, err := decodeASRFrame(data)
		if err != nil {
			e := &maas.Error{Kind: maas.KindProtocol, Message: "malformed ASR frame", Hint: "the server sent a frame this SDK cannot parse", Op: "asr.read", Err: err, RequestID: s.meta.RequestID}
			s.setError(e)
			s.signalFail(e)
			return
		}

		terminal, err := s.handleFrame(frame)
		if err != nil {
			s.setError(err)
			s.signalFail(err)
			s.emit(Event{Kind: EventError, Err: err, IsFinal: true})
			return
		}
		if terminal {
			return
		}
	}
}

// handleFrame processes one decoded frame and reports whether the session ended.
func (s *Session) handleFrame(f *asrFrame) (bool, error) {
	switch f.Type {
	case msgTypeError:
		return true, &maas.Error{
			Kind:      maas.KindAPI,
			Code:      fmt.Sprintf("%d", f.Code),
			Message:   "ASR session error",
			Hint:      asrErrorHint(f.Payload),
			RequestID: s.meta.RequestID,
			RawBody:   clipPayload(f.Payload, 1024),
			Op:        "asr.session",
		}
	case msgTypeFullServer:
		return s.handleResultFrame(f)
	default:
		// Unknown server frame types are ignored, matching the protocol rule that
		// unrecognised kinds must not be treated as completion.
		return false, nil
	}
}

// handleResultFrame parses a server result frame.
func (s *Session) handleResultFrame(f *asrFrame) (bool, error) {
	var w wireASRResponse
	if err := json.Unmarshal(f.Payload, &w); err != nil {
		return false, &maas.Error{Kind: maas.KindProtocol, Message: "cannot decode ASR payload", Op: "asr.session", Err: err, RequestID: s.meta.RequestID}
	}

	body, additions := w.bodyAndAdditions()
	kind := ""
	if additions != nil && additions.Parret != nil {
		kind = additions.Parret.Kind
	}

	ev := Event{
		Text:       body.text(),
		Utterances: body.utteranceList(),
		IsFinal:    w.IsLastPackage || f.Flags == flagNegativeSeq || kind == "terminal",
		Raw:        append(json.RawMessage(nil), f.Payload...),
	}
	if additions != nil {
		ev.LogID = additions.LogID
	}
	if w.PayloadMsg != nil && w.PayloadMsg.AudioInfo.Duration > 0 {
		ev.Duration = time.Duration(w.PayloadMsg.AudioInfo.Duration) * time.Millisecond
	}

	switch kind {
	case "ready":
		s.markReady()
		ev.Kind = EventReady
		s.emit(ev)
		return false, nil
	case "unit_settled":
		ev.Kind = EventUtteranceSettled
		s.emit(ev)
		return false, nil
	case "terminal":
		ev.Kind = EventTerminal
		if additions.Parret.Session != nil {
			ev.Status = additions.Parret.Session.Status
		}
		s.emit(ev)
		if !s.readyDone.Load() {
			s.signalFail(&maas.Error{
				Kind:      maas.KindAPI,
				Message:   "session ended before it became ready",
				Hint:      "the server rejected the configuration; check audio and request options",
				RequestID: s.meta.RequestID,
				Op:        "asr.Start",
			})
		}
		return true, nil
	}

	// No explicit kind: fall back to the frame flag to decide terminality.
	if ev.IsFinal {
		ev.Kind = EventTerminal
		ev.Status = "succeeded"
		if w.Code != 0 {
			ev.Status = "failed"
		}
		s.emit(ev)
		if !s.readyDone.Load() {
			s.signalFail(&maas.Error{Kind: maas.KindAPI, Message: "session ended before it became ready", Op: "asr.Start", RequestID: s.meta.RequestID})
		}
		return true, nil
	}

	ev.Kind = EventSnapshot
	s.emit(ev)
	return false, nil
}

// isASRSuccess reports whether a terminal status counts as success.
func isASRSuccess(status string) bool {
	return status == "succeeded"
}

// asrErrorHint maps a protocol reason to an actionable hint.
//
// The machine-readable reason is always included as a prefix so callers can log
// or branch on it without re-parsing the frame.
func asrErrorHint(payload []byte) string {
	var w struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Parret  *struct {
			Reason string `json:"reason"`
			Retry  string `json:"retry"`
		} `json:"parret"`
	}
	if err := json.Unmarshal(payload, &w); err != nil {
		return "inspect the error frame payload"
	}

	reason := ""
	if w.Parret != nil {
		reason = w.Parret.Reason
	}

	var hint string
	switch reason {
	case "invalid_request":
		hint = "frame structure, gzip or a field value is invalid; check the header layout and remove unknown fields"
	case "unsupported_option":
		hint = "an unsupported value was used; check format, language and other enumerations"
	case "configuration_rejected":
		hint = "hotword configuration was rejected; reduce or fix the entries"
	case "invalid_sequence":
		hint = "sequence numbers went backwards or skipped; the SDK manages these, so please report this"
	case "unusable_audio":
		hint = "audio was unusable; check that samples are 16 kHz/16-bit/mono"
	case "input_idle_timeout":
		hint = "no audio was accepted for too long; send audio continuously"
	case "capacity_exhausted":
		hint = "a frame exceeded 16000 samples or the concurrency quota is exhausted"
	case "input_after_end":
		hint = "audio was sent after the finish frame"
	default:
		if w.Code == maas.CodeHotwordTableNotPermitted {
			hint = "a hotword table is not authorised; check data.forbidden_table_ids"
		} else if reason != "" {
			hint = "the server rejected the session"
		} else {
			hint = "inspect the error frame payload"
		}
	}

	if reason != "" {
		return reason + ": " + hint
	}
	return hint
}

// wireASRResponse accommodates both documented response shapes: the gateway form
// (top-level result and additions) and the direct form (payload_msg wrapper).
type wireASRResponse struct {
	Code          int            `json:"code"`
	IsLastPackage bool           `json:"is_last_package"`
	PayloadMsg    *asrPayloadMsg `json:"payload_msg"`
	Result        *asrResultBody `json:"result"`
	Additions     *asrAdditions  `json:"additions"`
}

type asrPayloadMsg struct {
	AudioInfo struct {
		Duration int `json:"duration"`
	} `json:"audio_info"`
	Result *asrResultBody `json:"result"`
}

type asrResultBody struct {
	Text       string         `json:"text"`
	Utterances []asrUtterance `json:"utterances"`
	Additions  *asrAdditions  `json:"additions"`
}

func (b *asrResultBody) text() string {
	if b == nil {
		return ""
	}
	return b.Text
}

func (b *asrResultBody) utteranceList() []Utterance {
	if b == nil {
		return nil
	}
	out := make([]Utterance, 0, len(b.Utterances))
	for _, u := range b.Utterances {
		out = append(out, Utterance{
			Text:     u.Text,
			Definite: u.Definite,
			StartMS:  u.StartTime,
			EndMS:    u.EndTime,
		})
	}
	return out
}

type asrUtterance struct {
	Text      string `json:"text"`
	Definite  bool   `json:"definite"`
	StartTime int    `json:"start_time"`
	EndTime   int    `json:"end_time"`
}

type asrAdditions struct {
	LogID  string     `json:"log_id"`
	Parret *asrParret `json:"parret"`
}

type asrParret struct {
	Kind    string `json:"kind"`
	Reason  string `json:"reason"`
	Retry   string `json:"retry"`
	Session *struct {
		Status          string `json:"status"`
		AcceptedSamples int64  `json:"accepted_samples"`
		Units           int    `json:"units"`
	} `json:"session"`
}

// bodyAndAdditions picks the result and additions from either envelope shape.
func (w *wireASRResponse) bodyAndAdditions() (*asrResultBody, *asrAdditions) {
	body := w.Result
	additions := w.Additions
	if w.PayloadMsg != nil {
		if w.PayloadMsg.Result != nil {
			body = w.PayloadMsg.Result
		}
	}
	if body != nil && body.Additions != nil {
		additions = body.Additions
	}
	return body, additions
}

// Transcribe sends a whole file and returns the aggregated result.
//
// WAV input is detected by its RIFF header and sent as raw PCM; anything else is
// treated as raw PCM. The file is streamed, never buffered in full.
func (s *Service) Transcribe(ctx context.Context, file maas.FileSource, cfg Config) (*Result, error) {
	const op = "asr.Transcribe"

	if file == nil {
		return nil, maas.NewLocalError(op, "no file provided", "pass maas.File(...) to specify the audio")
	}

	rc, err := file.Open()
	if err != nil {
		return nil, &maas.Error{Kind: maas.KindLocal, Message: "cannot open audio file", Hint: "check the path", Op: op, Err: err}
	}
	defer func() { _ = rc.Close() }()

	br := bufio.NewReaderSize(rc, 64<<10)
	pcm, err := stripWAVHeader(br)
	if err != nil {
		return nil, &maas.Error{Kind: maas.KindLocal, Message: "cannot parse WAV header", Hint: "check the file, or pass raw PCM instead", Op: op, Err: err}
	}
	if cfg.Audio.Format == "" {
		cfg.Audio.Format = "pcm"
	}

	sess, err := s.Start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sess.Close() }()

	// Drain events so the read loop never blocks on a full channel.
	go func() {
		for range sess.Events() {
		}
	}()

	chunk := sess.ChunkSize() * 2 // 16-bit samples
	buf := make([]byte, chunk)
	for {
		n, readErr := io.ReadFull(pcm, buf)
		if n > 0 {
			if _, err := sess.Write(buf[:n]); err != nil {
				return nil, err
			}
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return nil, &maas.Error{Kind: maas.KindLocal, Message: "cannot read audio file", Op: op, Err: readErr}
		}
	}

	return sess.Finish(ctx)
}

// stripWAVHeader consumes a RIFF/WAVE header when present and returns the reader
// positioned at raw PCM samples.
func stripWAVHeader(br *bufio.Reader) (io.Reader, error) {
	head, err := br.Peek(12)
	if err != nil {
		// Too short to be a WAV container: treat it as raw samples.
		return br, nil
	}
	if string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return br, nil
	}
	if _, err := br.Discard(12); err != nil {
		return nil, err
	}
	for {
		chunkHead, err := br.Peek(8)
		if err != nil {
			return nil, fmt.Errorf("asr: truncated WAV chunk header: %w", err)
		}
		id := string(chunkHead[0:4])
		size := int(binary.LittleEndian.Uint32(chunkHead[4:8]))
		if _, err := br.Discard(8); err != nil {
			return nil, err
		}
		if id == "data" {
			return br, nil
		}
		if size < 0 {
			return nil, errors.New("asr: invalid WAV chunk size")
		}
		if _, err := br.Discard(size); err != nil {
			return nil, fmt.Errorf("asr: cannot skip WAV chunk %q: %w", id, err)
		}
	}
}
