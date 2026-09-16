package service

import "testing"

func TestConnectionHealthHelpers(t *testing.T) {
	if got := median([]int{20, 10, 30}); got != 20 {
		t.Errorf("median = %d, want 20", got)
	}
	if got := median(nil); got != 0 {
		t.Errorf("median(nil) = %d, want 0", got)
	}
	if got := jitter([]int{10, 40, 20}); got != 30 {
		t.Errorf("jitter = %d, want 30", got)
	}
	if got := lossPercent(5, 4); got != 20 {
		t.Errorf("loss = %f, want 20", got)
	}
	health := ConnectionHealth{LatencyMs: 40, JitterMs: 20, Received: 5, Sent: 5, Loss: 0}
	if got := healthQuality(health); got != "good" {
		t.Errorf("quality = %q, want good", got)
	}
	health.LatencyMs = 200
	if got := healthQuality(health); got != "degraded" {
		t.Errorf("quality = %q, want degraded", got)
	}
	health.LatencyMs = 500
	if got := healthQuality(health); got != "poor" {
		t.Errorf("quality = %q, want poor", got)
	}
}
