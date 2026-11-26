// Package ocr provides document, card, receipt and generic text recognition.
//
// All calls share one shape: you pass a file and get a result, regardless of
// whether the platform answers synchronously or creates a task to poll.
package ocr

import (
	"context"

	maas "github.com/hupeng12345/maas-client-go"
)

// Service exposes the OCR models.
type Service struct {
	c *maas.Client
}

// New builds a Service on top of a client.
func New(c *maas.Client) *Service { return &Service{c: c} }

// requestOptions is shared by every OCR call.
type requestOptions struct {
	fields   map[string]any
	async    bool
	wait     []WaitOption
	endpoint string
}

// RequestOption customises a single OCR call.
type RequestOption func(*requestOptions)

// ParseOption, RecognizeOption and OCROption are aliases of RequestOption, kept
// so godoc reads naturally for each family.
type (
	// ParseOption customises document parsing.
	ParseOption = RequestOption
	// RecognizeOption customises card recognition.
	RecognizeOption = RequestOption
	// OCROption customises generic OCR calls.
	OCROption = RequestOption
)

func (o *requestOptions) set(name string, value any) {
	if o.fields == nil {
		o.fields = map[string]any{}
	}
	o.fields[name] = value
}

// EnableTOC asks the parser to extract a table of contents.
func EnableTOC(enabled bool) RequestOption {
	return func(o *requestOptions) { o.set("enable_toc", enabled) }
}

// WithLanguage sets the document language hint (for example "ja", "de").
func WithLanguage(lang string) RequestOption {
	return func(o *requestOptions) { o.set("language", lang) }
}

// WithImageAnalysis asks the parser to analyse embedded images.
func WithImageAnalysis(enabled bool) RequestOption {
	return func(o *requestOptions) { o.set("image_analysis", enabled) }
}

// WithPageDetails asks the parser to return per-page detail.
func WithPageDetails(enabled bool) RequestOption {
	return func(o *requestOptions) { o.set("page_details", enabled) }
}

// WithFields adds arbitrary form fields.
//
// Use it for parameters added after this SDK release; unknown names are rejected
// by the server, so only pass documented ones.
func WithFields(fields map[string]any) RequestOption {
	return func(o *requestOptions) {
		for k, v := range fields {
			o.set(k, v)
		}
	}
}

// WithAsyncSubmit forces the asynchronous path even when a sync endpoint exists.
func WithAsyncSubmit() RequestOption {
	return func(o *requestOptions) { o.async = true }
}

// WithEndpoint overrides the request path for this call.
//
// Use it when a deployment exposes a different path than the default one, or to
// call a newly added endpoint before this SDK wraps it.
func WithEndpoint(path string) RequestOption {
	return func(o *requestOptions) {
		if path != "" {
			o.endpoint = path
		}
	}
}

// WithWaitOptions applies polling options when the call has to wait.
func WithWaitOptions(opts ...WaitOption) RequestOption {
	return func(o *requestOptions) { o.wait = append(o.wait, opts...) }
}

// pathOr returns the call-specific endpoint when set.
func (o *requestOptions) pathOr(fallback string) string {
	if o.endpoint != "" {
		return o.endpoint
	}
	return fallback
}

// resolveOptions applies options.
func resolveOptions(opts []RequestOption) *requestOptions {
	o := &requestOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

// runAsync submits to an async endpoint and waits for the result.
func (s *Service) runAsync(ctx context.Context, op, path string, file maas.FileSource, opts []RequestOption) (*VisionResult, error) {
	o := resolveOptions(opts)
	task, err := s.submitVision(ctx, op, o.pathOr(path), file, o.fields)
	if err != nil {
		return nil, err
	}
	return task.Wait(ctx, o.wait...)
}

// ParseDocument parses a document and returns the extracted content.
//
// The call submits a task and polls until it finishes, so callers never deal
// with task IDs unless they want to.
func (s *Service) ParseDocument(ctx context.Context, file maas.FileSource, opts ...ParseOption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.ParseDocument", pathParseGeneral, file, opts)
}

// ParseDocumentMultilang parses documents in the supported minority languages.
func (s *Service) ParseDocumentMultilang(ctx context.Context, file maas.FileSource, opts ...ParseOption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.ParseDocumentMultilang", pathParseMultilang, file, opts)
}

// ParseDocumentArabic parses Arabic documents.
func (s *Service) ParseDocumentArabic(ctx context.Context, file maas.FileSource, opts ...ParseOption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.ParseDocumentArabic", pathParseArabic, file, opts)
}

// ParseDocumentHunyuan parses documents with the Hunyuan OCR model.
func (s *Service) ParseDocumentHunyuan(ctx context.Context, file maas.FileSource, opts ...ParseOption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.ParseDocumentHunyuan", pathParseHunyuan, file, opts)
}

// SubmitDocument submits a parsing task and returns immediately.
func (s *Service) SubmitDocument(ctx context.Context, file maas.FileSource, opts ...ParseOption) (*Task, error) {
	o := resolveOptions(opts)
	return s.submitVision(ctx, "ocr.SubmitDocument", o.pathOr(pathParseGeneral), file, o.fields)
}

// Recognize runs card recognition.
//
// When a synchronous endpoint exists it is used by default, so no polling takes
// place. Pass WithAsyncSubmit to force the asynchronous path.
func (s *Service) Recognize(ctx context.Context, kind CardKind, file maas.FileSource, opts ...RecognizeOption) (*VisionResult, error) {
	if !kind.valid() {
		return nil, maas.NewLocalError("ocr.Recognize", "unknown card kind: "+string(kind), "use one of ocr.CardKinds()")
	}
	o := resolveOptions(opts)
	if o.async {
		return s.runAsync(ctx, "ocr.Recognize", kind.path(false), file, opts)
	}
	return s.invokeVision(ctx, "ocr.Recognize", o.pathOr(kind.path(true)), file, o.fields)
}

// RecognizeAsync always uses the asynchronous endpoint and returns the task.
func (s *Service) RecognizeAsync(ctx context.Context, kind CardKind, file maas.FileSource, opts ...RecognizeOption) (*Task, error) {
	if !kind.valid() {
		return nil, maas.NewLocalError("ocr.RecognizeAsync", "unknown card kind: "+string(kind), "use one of ocr.CardKinds()")
	}
	o := resolveOptions(opts)
	return s.submitVision(ctx, "ocr.RecognizeAsync", o.pathOr(kind.path(false)), file, o.fields)
}

// RecognizeBankStatement parses a bank statement.
func (s *Service) RecognizeBankStatement(ctx context.Context, file maas.FileSource, opts ...OCROption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.RecognizeBankStatement", pathBankStatement, file, opts)
}

// RecognizeMedicalReceipt parses a medical receipt.
func (s *Service) RecognizeMedicalReceipt(ctx context.Context, file maas.FileSource, opts ...OCROption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.RecognizeMedicalReceipt", pathMedicalReceipt, file, opts)
}

// RecognizeFinancialReceipt parses a financial receipt.
func (s *Service) RecognizeFinancialReceipt(ctx context.Context, file maas.FileSource, opts ...OCROption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.RecognizeFinancialReceipt", pathFinancialReceipt, file, opts)
}

// OCRText runs general text recognition.
func (s *Service) OCRText(ctx context.Context, file maas.FileSource, opts ...OCROption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.OCRText", pathOCRGeneral, file, opts)
}

// OCRTextPaddle runs Paddle-based text recognition.
func (s *Service) OCRTextPaddle(ctx context.Context, file maas.FileSource, opts ...OCROption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.OCRTextPaddle", pathOCRPaddle, file, opts)
}

// RecognizeSeal detects and recognises seals.
func (s *Service) RecognizeSeal(ctx context.Context, file maas.FileSource, opts ...OCROption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.RecognizeSeal", pathOCRSeal, file, opts)
}

// DetectPageDirection detects the orientation of page content.
func (s *Service) DetectPageDirection(ctx context.Context, file maas.FileSource, opts ...OCROption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.DetectPageDirection", pathOCRPageDirection, file, opts)
}

// DetectImageBlur scores how blurry an image is.
func (s *Service) DetectImageBlur(ctx context.Context, file maas.FileSource, opts ...OCROption) (*VisionResult, error) {
	return s.runAsync(ctx, "ocr.DetectImageBlur", pathImageBlur, file, opts)
}
