package ocr

import (
	"encoding/json"
	maas "github.com/hupeng12345/maas-client-go"
	"time"
)

// TaskStatus is the state of an asynchronous task.
type TaskStatus string

// Task states reported by the platform.
const (
	TaskPending    TaskStatus = "PENDING"
	TaskProcessing TaskStatus = "PROCESSING"
	TaskSuccess    TaskStatus = "SUCCESS"
	TaskFailed     TaskStatus = "FAILED"
	TaskCanceled   TaskStatus = "CANCELED"
)

// Terminal reports whether the status is final.
func (s TaskStatus) Terminal() bool {
	switch s {
	case TaskSuccess, TaskFailed, TaskCanceled:
		return true
	}
	// Unknown values are treated as terminal so polling cannot spin forever on a
	// state this SDK does not understand.
	return s != TaskPending && s != TaskProcessing && s != ""
}

// OK reports whether the task finished successfully.
func (s TaskStatus) OK() bool { return s == TaskSuccess }

// VisionResult is the outcome of an OCR-family call.
//
// It is returned for both synchronous and asynchronous endpoints; the task
// fields are empty for synchronous calls.
type VisionResult struct {
	// Meta carries the request ID, HTTP status and latency.
	Meta maas.ResponseMeta

	TaskUUID   string
	TaskStatus TaskStatus
	StartTime  int64
	EndTime    int64

	// MarkdownContent is the full document text in Markdown. Tables are HTML.
	MarkdownContent string
	// Pages holds per-page results.
	Pages []Page
	// TOC is present when the request asked for a table of contents.
	TOC *TOC

	// Raw is the original JSON, only filled when WithRawResult(true).
	Raw json.RawMessage
}

// Succeeded reports whether the task finished successfully.
func (r *VisionResult) Succeeded() bool { return r != nil && r.TaskStatus.OK() }

// Duration returns the reported processing time, or 0 when unknown.
func (r *VisionResult) Duration() time.Duration {
	if r == nil || r.StartTime == 0 || r.EndTime == 0 || r.EndTime < r.StartTime {
		return 0
	}
	return time.Duration(r.EndTime-r.StartTime) * time.Second
}

// Page is one page of a parsed document or one card image.
type Page struct {
	// Index is page_index, passed through as the server reported it. Note that
	// the platform documentation is inconsistent about whether it starts at 0 or
	// 1, so this SDK never adjusts it.
	Index int
	// Markdown is the page content in Markdown (page_markdown).
	Markdown string
	// Layout holds layout blocks (layout_result).
	Layout []LayoutBlock
	// IEResult holds extracted fields for card and receipt models (ie_result).
	IEResult []IEResult
	// Raw is the original page JSON, only filled when WithRawResult(true).
	Raw json.RawMessage
}

// LayoutBlock is a single layout element on a page.
type LayoutBlock struct {
	// Type is the block kind: text, image, table, chart, equation, list, code,
	// hyperlink and so on. Unknown values are passed through unchanged.
	Type string
	// PageIdx is the 1-based page number as reported by the server.
	PageIdx int
	// BBox is the normalised [x0,y0,x1,y1] box relative to the page, or nil.
	BBox []float64
	// Content is the block body (text, HTML table, LaTeX equation, ...).
	Content string
	// Metadata holds every remaining source field (text_level, img_path, ...).
	Metadata map[string]any
}

// IEResult is one extracted entity from a card or receipt model.
//
// Field names differ per card type, so Fields stays untyped; see the model
// documentation for the keys of each card kind.
type IEResult struct {
	Recognized bool
	Category   string
	BBox       []float64
	Angle      int
	Fields     map[string]any
	Raw        json.RawMessage
}

// TOC is a parsed table of contents.
type TOC struct {
	DocName   string
	Structure []TOCNode
}

// TOCNode is one chapter in a table of contents.
type TOCNode struct {
	Title      string
	NodeID     string
	Text       string
	StartIndex int
	EndIndex   int
	Children   []TOCNode
}

// ── wire types ─────────────────────────────────────────────

// wireVision is the JSON shape returned in the envelope data field.
type wireVision struct {
	TaskUUID        string     `json:"task_uuid"`
	TaskStatus      string     `json:"task_status"`
	StartTime       int64      `json:"start_time"`
	EndTime         int64      `json:"end_time"`
	MarkdownContent string     `json:"markdown_content"`
	Pages           []wirePage `json:"pages"`
	TOC             *TOC       `json:"toc"`
	RequestID       string     `json:"request_id"`
}

// wirePage mirrors one entry of pages.
type wirePage struct {
	PageIndex    *int         `json:"page_index"`
	PageMarkdown string       `json:"page_markdown"`
	LayoutResult []wireLayout `json:"layout_result"`
	IEResult     []wireIE     `json:"ie_result"`
}

// wireLayout mirrors one layout block.
type wireLayout struct {
	Type     string         `json:"type"`
	PageIdx  int            `json:"page_idx"`
	BBox     []float64      `json:"bbox"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata"`
}

// wireIE mirrors one extracted entity.
type wireIE struct {
	Recognized bool           `json:"recognized"`
	Category   string         `json:"category"`
	BBox       []float64      `json:"bbox"`
	Angle      int            `json:"angle"`
	Fields     map[string]any `json:"fields"`
}

// wireTaskSubmit is the payload of a task submission.
type wireTaskSubmit struct {
	TaskUUID   string `json:"task_uuid"`
	TaskStatus string `json:"task_status"`
	RequestID  string `json:"request_id"`
}

// toVisionResult converts the wire form into the public result.
func (w *wireVision) toVisionResult(meta maas.ResponseMeta, raw json.RawMessage, keepRaw bool) *VisionResult {
	if w == nil {
		return &VisionResult{Meta: meta}
	}
	out := &VisionResult{
		Meta:            meta,
		TaskUUID:        w.TaskUUID,
		TaskStatus:      TaskStatus(w.TaskStatus),
		StartTime:       w.StartTime,
		EndTime:         w.EndTime,
		MarkdownContent: w.MarkdownContent,
		TOC:             w.TOC,
	}
	if keepRaw {
		out.Raw = raw
	}
	for _, p := range w.Pages {
		page := Page{Markdown: p.PageMarkdown}
		if p.PageIndex != nil {
			page.Index = *p.PageIndex
		}
		for _, l := range p.LayoutResult {
			page.Layout = append(page.Layout, LayoutBlock{
				Type:     l.Type,
				PageIdx:  l.PageIdx,
				BBox:     l.BBox,
				Content:  l.Content,
				Metadata: l.Metadata,
			})
		}
		for _, ie := range p.IEResult {
			page.IEResult = append(page.IEResult, IEResult{
				Recognized: ie.Recognized,
				Category:   ie.Category,
				BBox:       ie.BBox,
				Angle:      ie.Angle,
				Fields:     ie.Fields,
			})
		}
		out.Pages = append(out.Pages, page)
	}
	return out
}
