//go:build windows

package mcp

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const activityTransportTimeout = 500 * time.Millisecond

type activityTransportDescriptor struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}

type activityTransportMessage struct {
	Token    string   `json:"token"`
	Activity Activity `json:"activity"`
}

// ActivitySocketReporter returns a best-effort authenticated Windows loopback reporter.
func ActivitySocketReporter(path string) func(Activity) {
	return func(activity Activity) {
		descriptorFile, err := os.Open(path)
		if err != nil {
			return
		}
		var descriptor activityTransportDescriptor
		err = json.NewDecoder(descriptorFile).Decode(&descriptor)
		_ = descriptorFile.Close()
		if err != nil || descriptor.Address == "" || descriptor.Token == "" {
			return
		}
		conn, err := net.DialTimeout("tcp", descriptor.Address, activityTransportTimeout)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(activityTransportTimeout))
		if json.NewEncoder(conn).Encode(activityTransportMessage{Token: descriptor.Token, Activity: activity}) != nil {
			return
		}
		var acknowledged [1]byte
		_, _ = conn.Read(acknowledged[:])
	}
}

// ActivitySocketPath returns Windows activity transport descriptor path for base URL.
func ActivitySocketPath(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Port() == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), "interseptor-activity-"+u.Port()+".json")
}
