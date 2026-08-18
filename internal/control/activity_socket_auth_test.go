package control

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/mcp"
)

func TestAuthenticatedActivityListenerRecordsWithCorrectToken(t *testing.T) {
	// Given
	h, _, _ := newHub(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	h.startActivityListener(listener, "correct-token")
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// When
	err = json.NewEncoder(conn).Encode(activitySocketMessage{
		Token:    "correct-token",
		Activity: mcp.Activity{Tool: "list_flows", OK: true},
	})

	// Then
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var acknowledged [1]byte
	_, _ = conn.Read(acknowledged[:])
	_ = conn.Close()
	h.closeActivitySocket()
	activities, err := (&metaAPI{h}).st.ListActivity(10)
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if len(activities) != 1 || activities[0].Tool != "list_flows" {
		t.Fatalf("activities = %+v, want one list_flows record", activities)
	}
}

func TestAuthenticatedActivityListenerRejectsWrongToken(t *testing.T) {
	// Given
	h, _, _ := newHub(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	h.startActivityListener(listener, "correct-token")
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// When
	err = json.NewEncoder(conn).Encode(activitySocketMessage{
		Token:    "wrong-token",
		Activity: mcp.Activity{Tool: "list_flows", OK: true},
	})

	// Then
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	_ = conn.Close()
	h.closeActivitySocket()
	activities, err := (&metaAPI{h}).st.ListActivity(10)
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if len(activities) != 0 {
		t.Fatalf("wrong token recorded activities: %+v", activities)
	}
}

func TestAuthenticatedActivityListenerRejectsOversizedCompleteMessage(t *testing.T) {
	h, _, _ := newHub(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.startActivityListener(listener, "correct-token")
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(activitySocketMessage{
		Token:    "correct-token",
		Activity: mcp.Activity{Tool: "list_flows", OK: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, bytes.Repeat([]byte(" "), maxActivitySocketMessage+1)...)
	_, _ = conn.Write(payload)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var acknowledged [1]byte
	n, _ := conn.Read(acknowledged[:])
	_ = conn.Close()
	h.closeActivitySocket()

	if n != 0 {
		t.Fatal("oversized activity message was acknowledged")
	}
	activities, err := (&metaAPI{h}).st.ListActivity(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 0 {
		t.Fatalf("oversized message recorded activities: %+v", activities)
	}
}

func TestAuthenticatedActivityListenerCloseReturnsWithIdleClient(t *testing.T) {
	// Given
	h, _, _ := newHub(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	h.startActivityListener(listener, "correct-token")
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	// When
	closed := make(chan struct{})
	go func() {
		h.closeActivitySocket()
		close(closed)
	}()

	// Then
	select {
	case <-closed:
	case <-time.After(time.Second):
		_ = client.Close()
		<-closed
		t.Fatal("closeActivitySocket blocked on idle authenticated client")
	}
}
