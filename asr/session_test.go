package asr

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	maas "github.com/hupeng12345/maas-client-go"
)

// fakeServer implements just enough of the protocol to exercise the client.
type fakeServer struct {
	t        *testing.T
	upgrader websocket.Upgrader

	mu       sync.Mutex
	gotInit  bool
	gotEnd   bool
	audioLen int
	headers  http.Header
	proto    string
	reply    func(f *fakeServer, conn *websocket.Conn, frame *asrFrame) bool
}

func newFakeServer(t *testing.T, reply func(f *fakeServer, conn *websocket.Conn, frame *asrFrame) bool) (*fakeServer, *httptest.Server) {
	f := &fakeServer{t: t, upgrader: websocket.Upgrader{}, reply: reply}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	conn, err := f.upgrader.Upgrade(w, r, nil)
	if err != nil {
		f.t.Errorf("upgrade: %v", err)
		return
	}
	defer conn.Close()

	f.mu.Lock()
	f.headers = r.Header.Clone()
	f.proto = r.Header.Get("Sec-WebSocket-Protocol")
	f.mu.Unlock()

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		frame, err := decodeASRFrame(data)
		if err != nil {
			f.t.Errorf("decode: %v", err)
			return
		}

		f.mu.Lock()
		switch {
		case frame.Type == msgTypeFullClient:
			f.gotInit = true
		case frame.Type == msgTypeAudioOnly && frame.Flags == flagNegativeSeq:
			f.gotEnd = true
		case frame.Type == msgTypeAudioOnly:
			f.audioLen += len(frame.Payload)
		}
		f.mu.Unlock()

		if f.reply(f, conn, frame) {
			return
		}
	}
}

// replyReady acknowledges the configuration.
func replyReady(f *fakeServer, conn *websocket.Conn, frame *asrFrame) bool {
	if frame.Type != msgTypeFullClient {
		return false
	}
	writeJSON(f.t, conn, flagPositiveSeq, 1, `{"result":{"text":""},"additions":{"parret":{"kind":"ready"}}}`)
	return false
}

// writeJSON sends one server frame with the given flags.
func writeJSON(t *testing.T, conn *websocket.Conn, flags byte, seq int32, payload string) {
	t.Helper()
	raw, err := encodeASRFrame(msgTypeFullServer, flags, seq, []byte(payload))
	if err != nil {
		t.Errorf("encode: %v", err)
		return
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		t.Errorf("write: %v", err)
	}
}

func newClient(t *testing.T, baseURL string) *maas.Client {
	t.Helper()
	c, err := maas.New(maas.WithAPIKey("sk_test"), maas.WithBaseURL(baseURL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func TestSessionHappyPath(t *testing.T) {
	fake, srv := newFakeServer(t, func(f *fakeServer, conn *websocket.Conn, frame *asrFrame) bool {
		if frame.Type == msgTypeFullClient {
			writeJSON(f.t, conn, flagPositiveSeq, 1, `{"result":{"text":""},"additions":{"parret":{"kind":"ready"}}}`)
			return false
		}
		if frame.Type == msgTypeAudioOnly && frame.Flags == flagNegativeSeq {
			writeJSON(f.t, conn, flagNegativeSeq, frame.Sequence,
				`{"result":{"text":"hello","utterances":[{"text":"hello","definite":true,"start_time":0,"end_time":500}]},`+
					`"additions":{"log_id":"log-1","parret":{"kind":"terminal","session":{"status":"succeeded"}}}}`)
			return true
		}
		return false
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := New(newClient(t, srv.URL)).Start(ctx, Config{Host: srv.URL, Request: Request{ShowUtterances: maas.Ptr(true)}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer sess.Close()

	if sess.RequestID() == "" {
		t.Fatal("request id must not be empty")
	}

	select {
	case ev := <-sess.Events():
		if ev.Kind != EventReady {
			t.Fatalf("first event should be ready, got %v", ev.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for ready")
	}

	if _, err := sess.Write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := sess.Finish(ctx)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if !res.Succeeded || res.Status != "succeeded" {
		t.Fatalf("want success, got %+v", res)
	}
	if res.Text != "hello" || res.LogID != "log-1" {
		t.Fatalf("result mismatch: %+v", res)
	}
	if len(res.Utterances) != 1 || res.Utterances[0].Text != "hello" {
		t.Fatalf("utterances mismatch: %+v", res.Utterances)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if !fake.gotInit || !fake.gotEnd {
		t.Fatalf("server did not see init/finish: init=%v finish=%v", fake.gotInit, fake.gotEnd)
	}
	if fake.audioLen != 4 {
		t.Fatalf("audio length mismatch: %d", fake.audioLen)
	}
	if got := fake.headers.Get(maas.HeaderAPIRequestID); len(got) != 36 {
		t.Fatalf("X-Api-Request-Id must be a UUID, got %q", got)
	}
	if got := fake.headers.Get("Authorization"); got != "Bearer sk_test" {
		t.Fatalf("authorization mismatch: %q", got)
	}
	if fake.proto != "" {
		t.Fatalf("no subprotocol must be negotiated, got %q", fake.proto)
	}
}

func TestSessionAuthForms(t *testing.T) {
	cases := []struct {
		name      string
		auth      Auth
		wantAuthz string
		wantKey   string
		wantRes   string
		wantApp   string
	}{
		{"bearer default", Auth{}, "Bearer sk_test", "", "", ""},
		{"bearer override", Auth{BearerKey: "other"}, "Bearer other", "", "", ""},
		{"direct", Auth{APIKey: "direct", ResourceID: "res-1"}, "", "direct", "res-1", ""},
		{"legacy", Auth{AppKey: "app", AccessKey: "access"}, "", "", "", "app"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake, srv := newFakeServer(t, replyReady)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			sess, err := New(newClient(t, srv.URL)).Start(ctx, Config{Host: srv.URL, Auth: tc.auth})
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			_ = sess.Close()

			fake.mu.Lock()
			defer fake.mu.Unlock()
			if got := fake.headers.Get("Authorization"); got != tc.wantAuthz {
				t.Errorf("Authorization = %q, want %q", got, tc.wantAuthz)
			}
			if got := fake.headers.Get("X-Api-Key"); got != tc.wantKey {
				t.Errorf("X-Api-Key = %q, want %q", got, tc.wantKey)
			}
			if got := fake.headers.Get("X-Api-Resource-Id"); got != tc.wantRes {
				t.Errorf("X-Api-Resource-Id = %q, want %q", got, tc.wantRes)
			}
			if got := fake.headers.Get("X-Api-App-Key"); got != tc.wantApp {
				t.Errorf("X-Api-App-Key = %q, want %q", got, tc.wantApp)
			}
		})
	}
}

func TestSessionErrorFrame(t *testing.T) {
	payload := []byte(`{"parret":{"reason":"unusable_audio"}}`)
	_, srv := newFakeServer(t, func(f *fakeServer, conn *websocket.Conn, frame *asrFrame) bool {
		if frame.Type != msgTypeFullClient {
			return false
		}
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(payload)
		_ = zw.Close()

		raw := make([]byte, asrErrorHeaderLen+buf.Len())
		raw[0] = frameVersion
		raw[1] = msgTypeError<<4 | flagPositiveSeq
		raw[2] = frameSerializationCompression
		binary.BigEndian.PutUint32(raw[4:8], 1)
		binary.BigEndian.PutUint32(raw[8:12], 45000002)
		binary.BigEndian.PutUint32(raw[12:16], uint32(buf.Len()))
		copy(raw[asrErrorHeaderLen:], buf.Bytes())
		_ = conn.WriteMessage(websocket.BinaryMessage, raw)
		return true
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := New(newClient(t, srv.URL)).Start(ctx, Config{Host: srv.URL})
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"45000002", "unusable_audio"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v does not contain %q", err, want)
		}
	}
}

// A connection that drops before the terminal frame means "unknown", not success.
func TestSessionDropsBeforeTerminal(t *testing.T) {
	_, srv := newFakeServer(t, func(f *fakeServer, conn *websocket.Conn, frame *asrFrame) bool {
		if frame.Type == msgTypeFullClient {
			writeJSON(f.t, conn, flagPositiveSeq, 1, `{"result":{"text":""},"additions":{"parret":{"kind":"ready"}}}`)
		}
		return false
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess, err := New(newClient(t, srv.URL)).Start(ctx, Config{Host: srv.URL})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer sess.Close()

	if _, err := sess.Finish(ctx); err == nil {
		t.Fatal("dropping before the terminal frame must not be reported as success")
	}
}

// Config.Host must be able to override the client endpoint for one session.
func TestSessionHostOverride(t *testing.T) {
	_, srv := newFakeServer(t, replyReady)
	c, err := maas.New(maas.WithAPIKey("sk_test"), maas.WithBaseURL("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess, err := New(c).Start(ctx, Config{Host: srv.URL})
	if err != nil {
		t.Fatalf("host override should bypass the client endpoint: %v", err)
	}
	_ = sess.Close()
}
