package maas

import (
	"context"
	"net/http"
	"testing"
)

func TestRequestIDFromHeaderVariants(t *testing.T) {
	h := http.Header{}
	h.Set("x-request-id", "lower")
	if got := RequestIDFromHeader(h); got != "lower" {
		t.Fatalf("lowercase header not read: %q", got)
	}

	h = http.Header{}
	h.Set("X-Request-Id", "mixed")
	if got := RequestIDFromHeader(h); got != "mixed" {
		t.Fatalf("mixed case header not read: %q", got)
	}

	// When the gateway does not echo a header, fall back to the ASR request ID header.
	h = http.Header{}
	h.Set(HeaderAPIRequestID, "uuid-1")
	if got := RequestIDFromHeader(h); got != "uuid-1" {
		t.Fatalf("api request id fallback failed: %q", got)
	}

	if got := RequestIDFromHeader(nil); got != "" {
		t.Fatalf("nil header should return empty, got %q", got)
	}
}

func TestRequestIDFromResponse(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("X-Request-Id", "from-server")
	if got := RequestID(resp); got != "from-server" {
		t.Fatalf("response request id mismatch: %q", got)
	}
	if got := RequestID(nil); got != "" {
		t.Fatalf("nil response should return empty, got %q", got)
	}
}

func TestIsUUID(t *testing.T) {
	valid := []string{
		"41169504-2acb-46d4-a59d-f4c1b1572240",
		"00000000-0000-4000-8000-000000000000",
		"ABCDEF01-2345-6789-ABCD-EF0123456789",
	}
	for _, s := range valid {
		if !isUUID(s) {
			t.Errorf("want valid uuid: %q", s)
		}
	}
	invalid := []string{
		"", "abc", "411695042acb46d4a59df4c1b1572240",
		"41169504-2acb-46d4-a59d-f4c1b157224",
		"41169504-2acb-46d4-a59d-f4c1b15722400",
		"41169504-2acb-46d4-a59d-f4c1b157224g",
	}
	for _, s := range invalid {
		if isUUID(s) {
			t.Errorf("want invalid uuid: %q", s)
		}
	}
}

func TestRequestIDsForASR(t *testing.T) {
	// No context ID: both IDs are generated and the ASR ID is a UUID.
	_, apiID, reqID := requestIDsForASR(context.Background())
	if !isUUID(apiID) {
		t.Fatalf("api request id must be uuid: %q", apiID)
	}
	if reqID == "" {
		t.Fatal("generic request id must not be empty")
	}

	// The context supplies a UUID: it is reused as-is.
	uuid := "41169504-2acb-46d4-a59d-f4c1b1572240"
	_, apiID, reqID = requestIDsForASR(WithRequestID(context.Background(), uuid))
	if apiID != uuid || reqID != uuid {
		t.Fatalf("uuid should be reused: api=%q req=%q", apiID, reqID)
	}

	// The context supplies a business ID (not a UUID): the ASR ID is generated
	// separately while the generic ID keeps the business value.
	_, apiID, reqID = requestIDsForASR(WithRequestID(context.Background(), "order-123"))
	if !isUUID(apiID) {
		t.Fatalf("api request id should be generated uuid, got %q", apiID)
	}
	if reqID != "order-123" {
		t.Fatalf("generic request id should be preserved, got %q", reqID)
	}
}

func TestResponseMetaString(t *testing.T) {
	if got := (ResponseMeta{}).String(); got != "" {
		t.Fatalf("empty meta should render empty, got %q", got)
	}
	if got := (ResponseMeta{RequestID: "r1"}).String(); got != "request_id=r1" {
		t.Fatalf("meta string mismatch: %q", got)
	}
}
