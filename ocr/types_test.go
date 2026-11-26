package ocr

import (
	"encoding/json"
	"testing"
	"time"

	maas "github.com/hupeng12345/maas-client-go"
)

// The wire-to-public conversion must preserve every documented field and pass
// unknown layout fields through metadata.
func TestWireToVisionResult(t *testing.T) {
	wire := &wireVision{
		TaskUUID:        "t1",
		TaskStatus:      "SUCCESS",
		StartTime:       100,
		EndTime:         105,
		MarkdownContent: "# doc",
		Pages: []wirePage{{
			PageIndex:    intPtr(1),
			PageMarkdown: "# page",
			LayoutResult: []wireLayout{{
				Type: "text", PageIdx: 1, BBox: []float64{0, 0, 1, 1},
				Content: "hello", Metadata: map[string]any{"text_level": 2},
			}},
			IEResult: []wireIE{{
				Recognized: true, Category: "ID", Angle: 90,
				Fields: map[string]any{"name": "Alice"},
			}},
		}},
		TOC: &TOC{DocName: "doc", Structure: []TOCNode{{Title: "one", StartIndex: 1, EndIndex: 2}}},
	}

	meta := maas.ResponseMeta{RequestID: "req-1", HTTPStatus: 200}
	got := wire.toVisionResult(meta, json.RawMessage(`{"raw":true}`), true)

	if got.Meta.RequestID != "req-1" {
		t.Errorf("meta: %+v", got.Meta)
	}
	if got.TaskUUID != "t1" || got.TaskStatus != TaskSuccess {
		t.Errorf("task: %+v", got)
	}
	if got.MarkdownContent != "# doc" {
		t.Errorf("markdown: %q", got.MarkdownContent)
	}
	if len(got.Pages) != 1 {
		t.Fatalf("pages: %d", len(got.Pages))
	}
	page := got.Pages[0]
	if page.Index != 1 || page.Markdown != "# page" {
		t.Errorf("page: %+v", page)
	}
	if len(page.Layout) != 1 || page.Layout[0].Content != "hello" {
		t.Errorf("layout: %+v", page.Layout)
	}
	if page.Layout[0].Metadata["text_level"] != 2 {
		t.Errorf("metadata: %+v", page.Layout[0].Metadata)
	}
	if len(page.IEResult) != 1 || page.IEResult[0].Fields["name"] != "Alice" {
		t.Errorf("ie: %+v", page.IEResult)
	}
	if got.TOC == nil || got.TOC.DocName != "doc" {
		t.Errorf("toc: %+v", got.TOC)
	}
	if len(got.Raw) == 0 {
		t.Error("raw should be retained when keepRaw is true")
	}
	if got.Duration() != 5*time.Second {
		t.Errorf("duration: %v", got.Duration())
	}
}

func TestWireToVisionResultWithoutRaw(t *testing.T) {
	got := (&wireVision{TaskStatus: "SUCCESS"}).toVisionResult(maas.ResponseMeta{}, json.RawMessage(`{"x":1}`), false)
	if got.Raw != nil {
		t.Error("raw must be dropped when keepRaw is false")
	}
}

func TestNilWire(t *testing.T) {
	var wire *wireVision
	got := wire.toVisionResult(maas.ResponseMeta{RequestID: "r"}, nil, false)
	if got == nil || got.Meta.RequestID != "r" {
		t.Fatalf("nil wire should still return an empty result: %+v", got)
	}
}

// Page index is passed through untouched because the platform is inconsistent
// about whether it starts at 0 or 1.
func TestPageIndexPassThrough(t *testing.T) {
	for _, idx := range []int{0, 1, 7} {
		wire := &wireVision{Pages: []wirePage{{PageIndex: intPtr(idx)}}}
		got := wire.toVisionResult(maas.ResponseMeta{}, nil, false)
		if got.Pages[0].Index != idx {
			t.Errorf("page index %d became %d", idx, got.Pages[0].Index)
		}
	}
}

func TestTaskStatusHelpers(t *testing.T) {
	cases := []struct {
		status   TaskStatus
		terminal bool
		ok       bool
	}{
		{TaskPending, false, false},
		{TaskProcessing, false, false},
		{TaskSuccess, true, true},
		{TaskFailed, true, false},
		{TaskCanceled, true, false},
		{TaskStatus("WEIRD"), true, false},
		{TaskStatus(""), false, false},
	}
	for _, tc := range cases {
		if got := tc.status.Terminal(); got != tc.terminal {
			t.Errorf("%q.Terminal() = %v, want %v", tc.status, got, tc.terminal)
		}
		if got := tc.status.OK(); got != tc.ok {
			t.Errorf("%q.OK() = %v, want %v", tc.status, got, tc.ok)
		}
	}
}

func TestCardKindsCopy(t *testing.T) {
	first := CardKinds()
	first[0] = "mutated"
	second := CardKinds()
	if second[0] == "mutated" {
		t.Fatal("CardKinds must return a defensive copy")
	}
}

func TestCardKindValidity(t *testing.T) {
	for _, kind := range CardKinds() {
		if !kind.valid() {
			t.Errorf("%q should be valid", kind)
		}
	}
	if CardKind("nope").valid() {
		t.Error("unknown kind must be invalid")
	}
}

func TestCardKindPaths(t *testing.T) {
	if got := CardIDCard.path(true); got != "/vision/v1/card/idcard/sync" {
		t.Errorf("sync path: %q", got)
	}
	if got := CardIDCard.path(false); got != "/vision/v1/card/idcard/async" {
		t.Errorf("async path: %q", got)
	}
}

func intPtr(v int) *int { return &v }
