package collauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"
)

// SigV4Params are the inputs for an AWS Signature V4 header signature.
type SigV4Params struct {
	AccessKey, SecretKey, SessionToken string
	Region, Service                    string
	Time                               time.Time
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func hmacSHA256(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// SignSigV4 signs req in place: it sets Host-bound X-Amz-Date, optional
// X-Amz-Security-Token, X-Amz-Content-Sha256 (s3 only) and Authorization.
func SignSigV4(req *Request, p SigV4Params) error {
	if p.AccessKey == "" || p.SecretKey == "" || p.Region == "" || p.Service == "" {
		return errors.New("sigv4 requires accessKey, secretKey, region and service")
	}
	u, err := url.Parse(req.URL)
	if err != nil || u.Host == "" {
		return errors.New("sigv4: invalid URL")
	}
	t := p.Time.UTC()
	amz := t.Format("20060102T150405Z")
	date := t.Format("20060102")
	req.Header.Set("X-Amz-Date", amz)
	if p.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", p.SessionToken)
	}
	payload := sha256hex(req.Body)
	if strings.EqualFold(p.Service, "s3") {
		req.Header.Set("X-Amz-Content-Sha256", payload)
	}
	req.Header.Del("Authorization")

	hdrs := map[string]string{"host": u.Host}
	for k, v := range req.Header {
		lk := strings.ToLower(k)
		if lk == "authorization" || lk == "user-agent" || lk == "connection" {
			continue
		}
		hdrs[lk] = strings.Join(trimAll(v), ",")
	}
	names := make([]string, 0, len(hdrs))
	for k := range hdrs {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHdr strings.Builder
	for _, n := range names {
		canonHdr.WriteString(n + ":" + hdrs[n] + "\n")
	}
	signed := strings.Join(names, ";")
	canon := strings.Join([]string{
		req.Method, canonicalPath(u), canonicalQuery(u), canonHdr.String(), signed, payload,
	}, "\n")
	scope := date + "/" + p.Region + "/" + p.Service + "/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amz + "\n" + scope + "\n" + sha256hex([]byte(canon))
	k := hmacSHA256([]byte("AWS4"+p.SecretKey), date)
	k = hmacSHA256(k, p.Region)
	k = hmacSHA256(k, p.Service)
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, sts))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+p.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
	return nil
}

func trimAll(vs []string) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = strings.Join(strings.Fields(v), " ")
	}
	return out
}

func canonicalPath(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	return p
}

func canonicalQuery(u *url.URL) string {
	q := u.Query()
	type kv struct{ k, v string }
	var all []kv
	for k, vs := range q {
		for _, v := range vs {
			all = append(all, kv{awsEscape(k), awsEscape(v)})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].k != all[j].k {
			return all[i].k < all[j].k
		}
		return all[i].v < all[j].v
	})
	parts := make([]string, len(all))
	for i, e := range all {
		parts[i] = e.k + "=" + e.v
	}
	return strings.Join(parts, "&")
}

func awsEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func (m *Manager) applySigV4(cfg Config, req *Request) (Result, error) {
	m.track(cfg.Fields["secretKey"], cfg.Fields["sessionToken"])
	err := SignSigV4(req, SigV4Params{
		AccessKey: cfg.f("accessKey"), SecretKey: cfg.Fields["secretKey"], SessionToken: cfg.f("sessionToken"),
		Region: cfg.f("region"), Service: cfg.f("service"), Time: m.now(),
	})
	if err != nil {
		return Result{}, m.fail(err)
	}
	return Result{Applied: "auth:awsv4"}, nil
}
