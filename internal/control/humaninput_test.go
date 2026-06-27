package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The AI's request_human_input call blocks until the human answers; the answer
// flows back, and the prompt leaves the pending list.
func TestHumanInputHandoff(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	type result struct {
		Answered bool   `json:"answered"`
		Answer   string `json:"answer"`
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Post(ts.URL+"/api/human-input", "application/json",
			strings.NewReader(`{"message":"fuzz ids 1-100?","options":["yes","no"]}`))
		if err != nil {
			resCh <- result{}
			return
		}
		defer resp.Body.Close()
		var p result
		json.NewDecoder(resp.Body).Decode(&p)
		resCh <- p
	}()

	// Wait for the prompt to register, then read its id from the pending list.
	var id int64
	for i := 0; i < 100 && id == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		r, _ := http.Get(ts.URL + "/api/human-input")
		var d struct {
			Prompts []struct {
				ID      int64  `json:"id"`
				Message string `json:"message"`
			} `json:"prompts"`
		}
		json.NewDecoder(r.Body).Decode(&d)
		r.Body.Close()
		if len(d.Prompts) == 1 {
			id = d.Prompts[0].ID
			if d.Prompts[0].Message != "fuzz ids 1-100?" {
				t.Fatalf("pending prompt message wrong: %q", d.Prompts[0].Message)
			}
		}
	}
	if id == 0 {
		t.Fatal("prompt did not register in the pending list")
	}

	// Human answers.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/human-input/"+strconv.FormatInt(id, 10)+"/respond",
		strings.NewReader(`{"answer":"yes, but stop at 50"}`))
	rr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	rr.Body.Close()
	if rr.StatusCode != http.StatusNoContent {
		t.Fatalf("respond status %d, want 204", rr.StatusCode)
	}

	// The blocked AI call returns with the human's answer.
	select {
	case res := <-resCh:
		if !res.Answered || res.Answer != "yes, but stop at 50" {
			t.Fatalf("AI got %+v, want answered with the text", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocked human-input call did not return after the human answered")
	}

	// It's no longer pending.
	r3, _ := http.Get(ts.URL + "/api/human-input")
	var d3 struct {
		Prompts []json.RawMessage `json:"prompts"`
	}
	json.NewDecoder(r3.Body).Decode(&d3)
	r3.Body.Close()
	if len(d3.Prompts) != 0 {
		t.Fatalf("answered prompt should not be pending, got %d", len(d3.Prompts))
	}
}
