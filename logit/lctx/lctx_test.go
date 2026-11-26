package lctx_test

import (
	"context"
	"testing"

	"github.com/hupeng12345/maas-client-go/logit/lctx"
)

// pairFormatter is a simple LogFormatter for tests.
type pairFormatter []lctx.Pair

func (p pairFormatter) Values() []lctx.Pair { return p }

func TestPushPairAndListPair(t *testing.T) {
	cases := []struct {
		name     string
		ctx      context.Context
		push     []lctx.LogFormatter
		wantKeys []string
	}{
		{"no formatters", context.Background(), nil, nil},
		{"empty formatter", context.Background(), []lctx.LogFormatter{pairFormatter{}}, nil},
		{
			name:     "single",
			ctx:      context.Background(),
			push:     []lctx.LogFormatter{pairFormatter{{Key: "a", Value: "1"}}},
			wantKeys: []string{"a"},
		},
		{
			name: "multiple formatters preserve order",
			ctx:  context.Background(),
			push: []lctx.LogFormatter{
				pairFormatter{{Key: "a", Value: "1"}},
				pairFormatter{{Key: "b", Value: "2"}, {Key: "c", Value: "3"}},
			},
			wantKeys: []string{"a", "b", "c"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := lctx.PushPair(tc.ctx, tc.push...)
			pairs := lctx.ListPair(ctx)
			if len(pairs) != len(tc.wantKeys) {
				t.Fatalf("got %d pairs, want %d", len(pairs), len(tc.wantKeys))
			}
			for i, key := range tc.wantKeys {
				if pairs[i].Key != key {
					t.Errorf("pair %d: got %q want %q", i, pairs[i].Key, key)
				}
			}
		})
	}
}

func TestPushPairAccumulates(t *testing.T) {
	ctx := lctx.PushPair(context.Background(), pairFormatter{{Key: "a", Value: "1"}})
	ctx = lctx.PushPair(ctx, pairFormatter{{Key: "b", Value: "2"}})

	pairs := lctx.ListPair(ctx)
	if len(pairs) != 2 || pairs[0].Key != "a" || pairs[1].Key != "b" {
		t.Fatalf("accumulation failed: %+v", pairs)
	}
}

func TestListPairNilContext(t *testing.T) {
	if got := lctx.ListPair(nil); got != nil {
		t.Fatalf("nil context should yield nil pairs, got %+v", got)
	}
}
