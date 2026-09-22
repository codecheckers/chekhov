package command

import (
	"strings"
	"testing"
)

// A register-wide failure is one problem per open issue. The reply says how
// many it left out rather than growing past what GitHub will accept.
func TestASweepReplyBoundsTheProblemsItLists(t *testing.T) {
	swept := Swept{Issues: 200}
	for range mostProblemsListed + 7 {
		swept.Problems = append(swept.Problems, "could not read the comments: 401")
	}

	reply := SweptReply(swept)

	if listed := strings.Count(reply, "Could not check:"); listed != mostProblemsListed {
		t.Errorf("listed %d problems, want %d:\n%s", listed, mostProblemsListed, reply)
	}
	if !strings.Contains(reply, "And 7 more like that") {
		t.Errorf("the reply does not say what it left out:\n%s", reply)
	}
}
