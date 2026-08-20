package classifier

import (
	"context"
	"testing"
	"time"
)

func TestDisabledClientReturnsNil(t *testing.T) {
	client, err := NewClassifierClient("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, err := client.Classify(context.Background(), "What is Kubernetes?", "test-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result from disabled client, got %+v", result)
	}
}

func TestMockClient(t *testing.T) {
	mock := &MockClassifierClient{
		Result: &ClassifyResult{
			ClassifierID: "complexity",
			Status:       "OK",
			TopLabel:     "SIMPLE",
			TopScore:     0.999,
			Margin:       1.074,
			Ranked: []RankedSignal{
				{Label: "SIMPLE", Score: 0.999},
				{Label: "MEDIUM", Score: -0.075},
			},
		},
	}

	result, err := mock.Classify(context.Background(), "What is Kubernetes?", "test-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TopLabel != "SIMPLE" {
		t.Fatalf("expected SIMPLE, got %s", result.TopLabel)
	}
	if mock.Calls != 1 {
		t.Fatalf("expected 1 call, got %d", mock.Calls)
	}
}

func TestMockClientWithError(t *testing.T) {
	mock := &MockClassifierClient{
		Err: context.DeadlineExceeded,
	}
	_, err := mock.Classify(context.Background(), "test", "test-3")
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestCacheHitAvoidsCalls(t *testing.T) {
	cache := NewClassificationCache(5 * time.Minute)
	mock := &MockClassifierClient{
		Result: &ClassifyResult{TopLabel: "REASONING", TopScore: 0.999},
	}

	text := "Prove P=NP"

	cache.Put(text, mock.Result)

	cached, ok := cache.Get(text)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if cached.TopLabel != "REASONING" {
		t.Fatalf("expected REASONING, got %s", cached.TopLabel)
	}
	if mock.Calls != 0 {
		t.Fatalf("expected 0 calls to mock, got %d", mock.Calls)
	}
}

func TestCacheMiss(t *testing.T) {
	cache := NewClassificationCache(5 * time.Minute)

	_, ok := cache.Get("never seen this prompt")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestCacheExpiry(t *testing.T) {
	cache := NewClassificationCache(1 * time.Millisecond)
	cache.Put("test", &ClassifyResult{TopLabel: "SIMPLE"})

	time.Sleep(5 * time.Millisecond)

	_, ok := cache.Get("test")
	if ok {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

func TestCacheReaper(t *testing.T) {
	cache := NewClassificationCache(1 * time.Millisecond)
	cache.Put("test1", &ClassifyResult{TopLabel: "SIMPLE"})
	cache.Put("test2", &ClassifyResult{TopLabel: "MEDIUM"})

	if cache.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", cache.Len())
	}

	time.Sleep(5 * time.Millisecond)
	cache.reap()

	if cache.Len() != 0 {
		t.Fatalf("expected 0 entries after reap, got %d", cache.Len())
	}
}

func TestMapResponse(t *testing.T) {
	tests := []struct {
		name     string
		ranked   []RankedSignal
		topLabel string
		margin   float64
	}{
		{
			name:     "normal 4 labels",
			ranked:   []RankedSignal{{Label: "REASONING", Score: 0.999}, {Label: "COMPLEX", Score: -0.232}},
			topLabel: "REASONING",
			margin:   1.231,
		},
		{
			name:     "single label",
			ranked:   []RankedSignal{{Label: "SIMPLE", Score: 0.5}},
			topLabel: "SIMPLE",
			margin:   0.5,
		},
		{
			name:     "empty ranked",
			ranked:   nil,
			topLabel: "",
			margin:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &ClassifyResult{Ranked: tt.ranked}
			if len(result.Ranked) > 0 {
				result.TopLabel = result.Ranked[0].Label
				result.TopScore = result.Ranked[0].Score
				if len(result.Ranked) > 1 {
					result.Margin = result.Ranked[0].Score - result.Ranked[1].Score
				} else {
					result.Margin = result.Ranked[0].Score
				}
			}
			if result.TopLabel != tt.topLabel {
				t.Errorf("TopLabel: got %s, want %s", result.TopLabel, tt.topLabel)
			}
			if diff := result.Margin - tt.margin; diff > 0.01 || diff < -0.01 {
				t.Errorf("Margin: got %.3f, want %.3f", result.Margin, tt.margin)
			}
		})
	}
}
