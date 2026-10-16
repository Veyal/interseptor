package collauth

import (
	"encoding/base64"
	"net/url"
)

func hasAuthz(req *Request) bool { return req.Header.Get("Authorization") != "" }

func (m *Manager) applyBasic(cfg Config, req *Request) (Result, error) {
	if hasAuthz(req) {
		return Result{}, nil
	}
	pw := cfg.Fields["password"]
	m.track(pw)
	cred := base64.StdEncoding.EncodeToString([]byte(cfg.Fields["username"] + ":" + pw))
	req.Header.Set("Authorization", "Basic "+cred)
	m.track(cred)
	return Result{Applied: "auth:basic"}, nil
}

func (m *Manager) applyBearer(cfg Config, req *Request) (Result, error) {
	if hasAuthz(req) {
		return Result{}, nil
	}
	prefix := cfg.f("prefix")
	if prefix == "" {
		prefix = "Bearer"
	}
	m.track(cfg.Fields["token"])
	req.Header.Set("Authorization", prefix+" "+cfg.Fields["token"])
	return Result{Applied: "auth:bearer"}, nil
}

func (m *Manager) applyAPIKey(cfg Config, req *Request) (Result, error) {
	k, v := cfg.f("key"), cfg.Fields["value"]
	if k == "" {
		return Result{}, nil
	}
	m.track(v)
	if cfg.f("in") == "query" {
		return Result{Applied: "auth:apikey"}, addQuery(req, k, v)
	}
	if req.Header.Get(k) == "" {
		req.Header.Set(k, v)
	}
	return Result{Applied: "auth:apikey"}, nil
}

func addQuery(req *Request, k, v string) error {
	u, err := url.Parse(req.URL)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set(k, v)
	u.RawQuery = q.Encode()
	req.URL = u.String()
	return nil
}
