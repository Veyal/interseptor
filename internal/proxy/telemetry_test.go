package proxy

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

func TestIsBrowserTelemetry(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"incoming.telemetry.mozilla.org", true},
		{"Incoming.Telemetry.Mozilla.Org:443", true},
		{"Incoming.Telemetry.Mozilla.Org.:443", true},
		{"firefox-portal-detection.com", true},
		{"content-signature-2.cdn.mozilla.net", true},
		{"classify-client.services.mozilla.com", true},
		{"merino.services.mozilla.com", true},
		{"ohttp-gateway-merino.services.mozilla.com", true},
		{"ohttp-merino.mozilla.fastly-edge.com", true},
		{"mozilla-ohttp.fastly-edge.com", true},
		{"mozilla-ohttp-dap.mozilla.fastly-edge.com", true},
		{"prod.ohttp-gateway.prod.webservices.mozgcp.net", true},
		{"prod-games-particle.merino.prod.webservices.mozgcp.net", true},
		{"prod-images.merino.prod.webservices.mozgcp.net", true},
		{"dap.services.mozilla.com", true},
		{"dap-09-3.api.divviup.org", true},
		{"coverage.mozilla.org", true},
		{"contile.services.mozilla.com", true},
		{"contile-images.services.mozilla.com", true},
		{"ads.mozilla.org", true},
		{"ads-img.mozilla.org", true},
		{"spocs.getpocket.com", true},
		{"webextensions.settings.services.mozilla.com", true},
		{"crash-stats.mozilla.com", false},
		{"crash-stats.mozilla.org", false},
		{"safebrowsing.googleapis.com", true},
		{"safebrowsing.google.com", true},
		{"sb-ssl.google.com", true},
		{"example.com", false},
		{"telemetry.example.com", false},
		{"accounts.firefox.com", false},
		{"services.addons.mozilla.org", false},
		{"download.mozilla.org", false},
		{"example.mozilla.org", false},
		{"api.divviup.org", false},
		{"example.fastly-edge.com", false},
		{"example.prod.webservices.mozgcp.net", false},
		{"authorized.merino.prod.webservices.mozgcp.net", false},
		{"authorized.ohttp-gateway.prod.webservices.mozgcp.net", false},
		{"[2001:db8::1]:443", false},
	}
	for _, c := range cases {
		if got := isBrowserTelemetry(c.host); got != c.want {
			t.Errorf("isBrowserTelemetry(%q)=%v, want %v", c.host, got, c.want)
		}
	}
}

func TestSuppressedTelemetrySkipsResponseRulesAndHold(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		enable func(*Server)
	}{
		{
			name: "browser background traffic",
			host: "merino.services.mozilla.com",
			enable: func(s *Server) {
				s.SetSuppressBrowserTelemetry(true)
			},
		},
		{
			name: "android telemetry",
			host: "reports.crashlytics.com",
			enable: func(s *Server) {
				s.SetSuppressAndroidTelemetry(true)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng := intercept.New()
			if err := eng.SetRules([]store.Rule{{
				Enabled: true,
				Type:    "res-header",
				Match:   `Server: .*`,
				Replace: "Server: redacted",
			}}); err != nil {
				t.Fatalf("SetRules: %v", err)
			}
			eng.SetResponseEnabled(true)
			srv := &Server{eng: eng}
			tt.enable(srv)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest(http.MethodGet, "https://"+tt.host+"/background", nil).WithContext(ctx)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Server": {"origin"}},
				Body:       io.NopCloser(strings.NewReader("unchanged")),
				Request:    req,
			}

			flow := srv.newFlow(&store.Flow{Host: tt.host})
			_, _, _, transformed, dropped := srv.maybeInterceptResponse(flow, resp)
			if transformed || dropped {
				t.Fatalf("suppressed response transformed=%v dropped=%v, want untouched forwarding", transformed, dropped)
			}
			if got := len(eng.ResponseQueue()); got != 0 {
				t.Fatalf("suppressed response queue length=%d, want 0", got)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read untouched response: %v", err)
			}
			if got := string(body); got != "unchanged" {
				t.Fatalf("suppressed response body=%q, want original body", got)
			}
		})
	}
}

func TestBrowserBackgroundSuppressionSeparatesTargetTraffic(t *testing.T) {
	requestUserAgent := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestUserAgent <- r.Header.Get("User-Agent")
		w.Header().Set("Server", "origin")
		io.WriteString(w, "unchanged")
	}))
	defer upstream.Close()
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	eng := intercept.New()
	if err := eng.SetRules([]store.Rule{
		{Enabled: true, Type: "req-header", Match: `User-Agent: .*`, Replace: "User-Agent: rewritten"},
		{Enabled: true, Type: "res-header", Match: `Server: .*`, Replace: "Server: rewritten"},
	}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	srv := New(st, capture.New(st), nil, eng, nil)
	srv.SetSuppressBrowserTelemetry(true)
	srv.tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstreamURL.Host)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go srv.Serve(listener)

	proxyURL, err := url.Parse("http://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}
	req, err := http.NewRequest(http.MethodGet, "http://merino.services.mozilla.com/background", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("User-Agent", "original")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if got := string(body); got != "unchanged" {
		t.Fatalf("response body=%q, want unchanged", got)
	}
	if got := resp.Header.Get("Server"); got != "origin" {
		t.Fatalf("response rule changed suppressed traffic: Server=%q", got)
	}
	select {
	case got := <-requestUserAgent:
		if got != "original" {
			t.Fatalf("request rule changed suppressed traffic: User-Agent=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive suppressed request")
	}
	if flows, err := st.QueryFlows(10); err != nil {
		t.Fatalf("query flows: %v", err)
	} else if len(flows) != 0 {
		t.Fatalf("suppressed browser background traffic created %d history rows", len(flows))
	}
	if got := len(eng.Queue()); got != 0 {
		t.Fatalf("suppressed request queue length=%d, want 0", got)
	}
	if got := len(eng.ResponseQueue()); got != 0 {
		t.Fatalf("suppressed response queue length=%d, want 0", got)
	}
	if entries, err := os.ReadDir(st.BodiesDir()); err != nil {
		t.Fatalf("read body store: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("suppressed browser background traffic created %d body-store entries", len(entries))
	}

	targetReq, err := http.NewRequest(http.MethodPost, "http://example.com/authorized-target", strings.NewReader("normal-request"))
	if err != nil {
		t.Fatalf("new target request: %v", err)
	}
	targetReq.Header.Set("User-Agent", "original")
	targetResp, err := client.Do(targetReq)
	if err != nil {
		t.Fatalf("target request through proxy: %v", err)
	}
	if _, err := io.Copy(io.Discard, targetResp.Body); err != nil {
		t.Fatalf("read target response: %v", err)
	}
	targetResp.Body.Close()
	if got := targetResp.Header.Get("Server"); got != "rewritten" {
		t.Fatalf("response rule skipped normal target traffic: Server=%q", got)
	}
	select {
	case got := <-requestUserAgent:
		if got != "rewritten" {
			t.Fatalf("request rule skipped normal target traffic: User-Agent=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive normal target request")
	}
	flows, err := st.QueryFlows(10)
	if err != nil {
		t.Fatalf("query normal target flow: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("normal target created %d history rows, want 1", len(flows))
	}
	if flow := flows[0]; flow.Host != "example.com" || flow.ReqBodyHash == "" || flow.ResBodyHash == "" {
		t.Fatalf("normal target capture=%+v, want host and both body hashes", flow)
	}
	if entries, err := os.ReadDir(st.BodiesDir()); err != nil {
		t.Fatalf("read normal target body store: %v", err)
	} else if len(entries) == 0 {
		t.Fatal("normal target traffic did not create body-store entries")
	}
}

func TestBrowserSuppressionDecisionRemainsStableAcrossToggle(t *testing.T) {
	tests := []struct {
		name             string
		initial          bool
		wantUserAgent    string
		wantServerHeader string
		wantCaptured     bool
	}{
		{name: "suppressed admission", initial: true, wantUserAgent: "original", wantServerHeader: "origin"},
		{name: "captured admission", initial: false, wantUserAgent: "rewritten", wantServerHeader: "rewritten", wantCaptured: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received := make(chan string, 1)
			release := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				received <- r.Header.Get("User-Agent")
				<-release
				w.Header().Set("Server", "origin")
				_, _ = io.WriteString(w, "response-body")
			}))
			defer upstream.Close()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			upstreamURL, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatalf("parse upstream URL: %v", err)
			}

			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			defer st.Close()
			eng := intercept.New()
			if err := eng.SetRules([]store.Rule{
				{Enabled: true, Type: "req-header", Match: `User-Agent: .*`, Replace: "User-Agent: rewritten"},
				{Enabled: true, Type: "res-header", Match: `Server: .*`, Replace: "Server: rewritten"},
			}); err != nil {
				t.Fatalf("set rules: %v", err)
			}
			events := &recEvents{}
			srv := New(st, capture.New(st), nil, eng, events)
			srv.SetSuppressBrowserTelemetry(tt.initial)
			srv.tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, upstreamURL.Host)
			}

			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer listener.Close()
			go srv.Serve(listener)

			proxyURL, err := url.Parse("http://" + listener.Addr().String())
			if err != nil {
				t.Fatalf("parse proxy URL: %v", err)
			}
			client := &http.Client{
				Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
				Timeout:   5 * time.Second,
			}
			type result struct {
				resp *http.Response
				body []byte
				err  error
			}
			resultCh := make(chan result, 1)
			go func() {
				req, reqErr := http.NewRequest(http.MethodPost, "http://merino.services.mozilla.com/background", strings.NewReader("request-body"))
				if reqErr != nil {
					resultCh <- result{err: reqErr}
					return
				}
				req.Header.Set("User-Agent", "original")
				resp, reqErr := client.Do(req)
				if reqErr != nil {
					resultCh <- result{err: reqErr}
					return
				}
				body, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()
				resultCh <- result{resp: resp, body: body, err: readErr}
			}()

			select {
			case got := <-received:
				if got != tt.wantUserAgent {
					t.Fatalf("upstream User-Agent=%q, want %q", got, tt.wantUserAgent)
				}
			case <-time.After(time.Second):
				t.Fatal("upstream did not receive request")
			}
			srv.SetSuppressBrowserTelemetry(!tt.initial)
			close(release)

			var got result
			select {
			case got = <-resultCh:
			case <-time.After(2 * time.Second):
				t.Fatal("proxy did not finish toggled request")
			}
			if got.err != nil {
				t.Fatalf("request through proxy: %v", got.err)
			}
			if got.resp.Header.Get("Server") != tt.wantServerHeader {
				t.Fatalf("response Server=%q, want %q", got.resp.Header.Get("Server"), tt.wantServerHeader)
			}
			if string(got.body) != "response-body" {
				t.Fatalf("response body=%q", got.body)
			}

			flows, err := st.QueryFlows(10)
			if err != nil {
				t.Fatalf("query flows: %v", err)
			}
			captured, updated := events.snap()
			entries, err := os.ReadDir(st.BodiesDir())
			if err != nil {
				t.Fatalf("read body store: %v", err)
			}
			if tt.wantCaptured {
				if len(flows) != 1 || flows[0].ReqBodyHash == "" || flows[0].ResBodyHash == "" {
					t.Fatalf("captured flow=%+v, want request and response bodies", flows)
				}
				if len(captured) != 1 || len(updated) != 1 || len(entries) == 0 {
					t.Fatalf("capture side effects: new=%d update=%d bodies=%d", len(captured), len(updated), len(entries))
				}
			} else if len(flows) != 0 || len(captured) != 0 || len(updated) != 0 || len(entries) != 0 {
				t.Fatalf("suppressed side effects: flows=%d new=%d update=%d bodies=%d", len(flows), len(captured), len(updated), len(entries))
			}
		})
	}
}

type websocketEventRecorder struct {
	recEvents
	frames chan int64
}

func (r *websocketEventRecorder) WSFramed(flowID int64) {
	r.frames <- flowID
}

func TestSuppressedUpgradeUsesRawRelayWithoutCapture(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	events := &websocketEventRecorder{frames: make(chan int64, 1)}
	srv := New(st, capture.New(st), nil, nil, events)
	srv.SetSuppressBrowserTelemetry(true)
	flow := srv.newFlow(&store.Flow{Host: "push.services.mozilla.com", Scheme: "https", Port: 443})
	srv.record(flow)

	clientProxy, clientPeer := net.Pipe()
	upstreamProxy, upstreamPeer := net.Pipe()
	defer clientPeer.Close()
	defer upstreamPeer.Close()
	_ = clientPeer.SetDeadline(time.Now().Add(2 * time.Second))
	_ = upstreamPeer.SetDeadline(time.Now().Add(2 * time.Second))
	done := make(chan struct{})
	go func() {
		srv.relayUpgrade(flow, bufio.NewReader(clientProxy), bufio.NewReader(upstreamProxy), clientProxy, upstreamProxy)
		close(done)
	}()

	clientFrame := wsTextFrame("client-frame", true)
	writeResult := make(chan error, 1)
	go func() {
		_, err := clientPeer.Write(clientFrame)
		writeResult <- err
	}()
	forwarded := make([]byte, len(clientFrame))
	if _, err := io.ReadFull(upstreamPeer, forwarded); err != nil {
		t.Fatalf("read upstream frame: %v", err)
	}
	if err := <-writeResult; err != nil {
		t.Fatalf("write client frame: %v", err)
	}
	if !bytes.Equal(forwarded, clientFrame) {
		t.Fatalf("client frame changed in raw relay")
	}

	serverFrame := wsTextFrame("server-frame", false)
	go func() {
		_, err := upstreamPeer.Write(serverFrame)
		writeResult <- err
	}()
	forwarded = make([]byte, len(serverFrame))
	if _, err := io.ReadFull(clientPeer, forwarded); err != nil {
		t.Fatalf("read client frame: %v", err)
	}
	if err := <-writeResult; err != nil {
		t.Fatalf("write server frame: %v", err)
	}
	if !bytes.Equal(forwarded, serverFrame) {
		t.Fatalf("server frame changed in raw relay")
	}

	clientPeer.Close()
	upstreamPeer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("raw upgrade relay did not stop")
	}
	frames, err := st.QueryWSFrames(0, 10)
	if err != nil {
		t.Fatalf("query websocket frames: %v", err)
	}
	flows, err := st.QueryFlows(10)
	if err != nil {
		t.Fatalf("query flows: %v", err)
	}
	captured, updated := events.snap()
	if len(frames) != 0 || len(flows) != 0 || len(captured) != 0 || len(updated) != 0 {
		t.Fatalf("suppressed upgrade side effects: frames=%d flows=%d new=%d update=%d", len(frames), len(flows), len(captured), len(updated))
	}
	select {
	case flowID := <-events.frames:
		t.Fatalf("suppressed upgrade emitted ws.frame for flow %d", flowID)
	default:
	}
}

func TestIsAndroidTelemetry(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"android.clients.google.com", true},
		{"Android.Clients.Google.Com:443", true},
		{"play.googleapis.com", true},
		{"connectivitycheck.gstatic.com", true},
		{"crashlyticsreports-pa.googleapis.com", true},
		{"reports.crashlytics.com", true},
		{"firebase-settings.crashlytics.com", true},
		{"region1.app-measurement.com", true},
		{"app-measurement.com", true},
		{"firebaselogging-pa.googleapis.com", true},
		{"googleads.g.doubleclick.net", true},
		// Must not suppress app backends / auth / push that testers need.
		{"example.com", false},
		{"api.example.com", false},
		{"firebase.googleapis.com", false},
		{"firestore.googleapis.com", false},
		{"accounts.google.com", false},
		{"mtalk.google.com", false},
		{"www.googleapis.com", false},
	}
	for _, c := range cases {
		if got := isAndroidTelemetry(c.host); got != c.want {
			t.Errorf("isAndroidTelemetry(%q)=%v, want %v", c.host, got, c.want)
		}
	}
}

func TestPersistableSuppressesAndroidTelemetry(t *testing.T) {
	s := &Server{}
	flow := s.newFlow(&store.Flow{Host: "android.clients.google.com", Port: 443})

	// Default atomic false: still persist until cmd wires the setting on.
	if !s.persistable(flow) {
		t.Fatal("android telemetry should persist when suppression is off")
	}

	s.SetSuppressAndroidTelemetry(true)
	if !s.persistable(flow) {
		t.Fatal("an admitted flow must keep its original capture decision")
	}
	flow = s.newFlow(&store.Flow{Host: "android.clients.google.com", Port: 443})
	if s.persistable(flow) {
		t.Fatal("android telemetry must be dropped when suppression is on")
	}

	// Ordinary app host still persists.
	app := s.newFlow(&store.Flow{Host: "api.example.com", Port: 443})
	if !s.persistable(app) {
		t.Fatal("non-telemetry host must still persist")
	}

	// Browser suppress must not affect Android hosts (and vice versa).
	s.SetSuppressAndroidTelemetry(false)
	s.SetSuppressBrowserTelemetry(true)
	flow = s.newFlow(&store.Flow{Host: "android.clients.google.com", Port: 443})
	if !s.persistable(flow) {
		t.Fatal("browser-only suppress must not drop android hosts")
	}
	browser := s.newFlow(&store.Flow{Host: "incoming.telemetry.mozilla.org", Port: 443})
	if s.persistable(browser) {
		t.Fatal("browser telemetry must still be dropped by browser suppress")
	}
	for _, candidate := range []*store.Flow{
		{Host: "merino.services.mozilla.com", Port: 443, HTTPVersion: "HTTP/1.1"},
		{Host: "mozilla-ohttp.fastly-edge.com", Port: 443, HTTPVersion: "HTTP/2.0"},
		{Host: "firefox-portal-detection.com.", Port: 80, HTTPVersion: "HTTP/1.1"},
	} {
		flow := s.newFlow(candidate)
		if s.persistable(flow) {
			t.Errorf("browser background flow %q (%s) must be dropped", flow.Host, flow.HTTPVersion)
		}
	}
}
