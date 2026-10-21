// Package client is a small, hand-written client for the parts of the Kubermatic Kubernetes Platform (KKP)
// REST API that the provider manages. Request and response shapes follow the real KKP API server
// (k8c.io/dashboard modules/api), which differs from swagger.json in a few places noted next to the calls.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config configures a Client.
type Config struct {
	// Host is the KKP base URL, e.g. https://kkp.example.com. A trailing /api is accepted and dropped.
	Host string
	// Token is sent as "Authorization: Bearer <token>": a project service account token, or any token the
	// KKP API accepts (an OIDC ID token works too, but is short-lived).
	Token              string
	CACertPEM          string
	InsecureSkipVerify bool
	UserAgent          string
	// MaxRetries is how often idempotent requests are retried on 429, 5xx and network errors.
	MaxRetries int
	// HTTPClient overrides the transport; tests use it.
	HTTPClient *http.Client
}

// Client talks to one KKP API endpoint.
type Client struct {
	base       *url.URL
	token      string
	userAgent  string
	maxRetries int
	http       *http.Client
	// retryWait is the first backoff; tests shorten it.
	retryWait time.Duration
}

// New validates cfg and returns a Client. It makes no requests.
func New(cfg Config) (*Client, error) {
	if cfg.Host == "" {
		return nil, errors.New("host is required")
	}
	u, err := url.Parse(strings.TrimRight(cfg.Host, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("host %q is not an absolute URL", cfg.Host)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("host %q must use http or https", cfg.Host)
	}
	u.Path = strings.TrimSuffix(u.Path, "/api")
	if cfg.Token == "" {
		return nil, errors.New("token is required")
	}

	hc := cfg.HTTPClient
	if hc == nil {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // opt-in for lab installs
		if cfg.CACertPEM != "" {
			pool, err := x509.SystemCertPool()
			if err != nil || pool == nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM([]byte(cfg.CACertPEM)) {
				return nil, errors.New("ca_certificate does not contain a PEM certificate")
			}
			tlsCfg.RootCAs = pool
		}
		tr := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tlsCfg, ForceAttemptHTTP2: true,
			MaxIdleConns: 10, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second}
		hc = &http.Client{Transport: tr, Timeout: 2 * time.Minute}
	}
	retries := cfg.MaxRetries
	if retries < 0 {
		retries = 0
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "terraform-provider-kkp"
	}
	return &Client{base: u, token: strings.TrimSpace(cfg.Token), userAgent: ua, maxRetries: retries, http: hc, retryWait: time.Second}, nil
}

// SetRetryWait changes the first retry backoff (tests).
func (c *Client) SetRetryWait(d time.Duration) { c.retryWait = d }

// Host returns the base URL the client talks to.
func (c *Client) Host() string { return c.base.String() }

// TokenProjectID returns the project a KKP service account token is bound to, read from the token's
// unverified JWT claims ("project_id", see modules/api/pkg/serviceaccount/jwt.go upstream). It returns ""
// for anything else, e.g. OIDC ID tokens. The server still verifies the token on every request.
func (c *Client) TokenProjectID() string { return TokenProjectID(c.token) }

// TokenProjectID is the function behind Client.TokenProjectID.
func TokenProjectID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		ProjectID string `json:"project_id"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.ProjectID
}

// APIError is a non-2xx answer from KKP. KKP errors look like {"error":{"code":403,"message":"…","details":[…]}}.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Message    string
	Details    []string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if len(e.Details) > 0 {
		msg += " (" + strings.Join(e.Details, "; ") + ")"
	}
	return fmt.Sprintf("KKP API %s %s: %d %s", e.Method, e.Path, e.StatusCode, msg)
}

// IsNotFound reports whether err is a 404 from KKP.
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsForbidden reports whether err is a 403 from KKP.
func IsForbidden(err error) bool { return hasStatus(err, http.StatusForbidden) }

// IsUnavailable reports whether err is a 503, which KKP answers while a cluster's control plane is not ready
// ("Cluster components are not ready yet").
func IsUnavailable(err error) bool { return hasStatus(err, http.StatusServiceUnavailable) }

// IsConflict reports whether err is a 409.
func IsConflict(err error) bool { return hasStatus(err, http.StatusConflict) }

func hasStatus(err error, code int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == code
}

func parseError(method, path string, status int, body []byte) error {
	e := &APIError{Method: method, Path: path, StatusCode: status}
	var wrapped struct {
		Error struct {
			Message string   `json:"message"`
			Details []string `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &wrapped) == nil && wrapped.Error.Message != "" {
		e.Message, e.Details = wrapped.Error.Message, wrapped.Error.Details
	} else if s := strings.TrimSpace(string(body)); s != "" && len(s) < 500 {
		e.Message = s
	}
	return e
}

type request struct {
	method      string
	path        string
	query       url.Values
	body        any
	contentType string
	headers     map[string]string
}

// do sends req and decodes a JSON answer into out (when out is non-nil). Raw []byte answers go to *[]byte.
func (c *Client) do(ctx context.Context, req request, out any) error {
	var payload []byte
	if req.body != nil {
		var err error
		if payload, err = json.Marshal(req.body); err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
	}
	u := *c.base
	u.Path += req.path
	if len(req.query) > 0 {
		u.RawQuery = req.query.Encode()
	}

	// Writes are retried only on answers that mean the server never acted on them.
	idempotent := req.method == http.MethodGet || req.method == http.MethodPut || req.method == http.MethodDelete
	wait := c.retryWait
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		hr, err := http.NewRequestWithContext(ctx, req.method, u.String(), body)
		if err != nil {
			return err
		}
		hr.Header.Set("Authorization", "Bearer "+c.token)
		hr.Header.Set("Accept", "application/json")
		hr.Header.Set("User-Agent", c.userAgent)
		if payload != nil {
			ct := req.contentType
			if ct == "" {
				ct = "application/json"
			}
			hr.Header.Set("Content-Type", ct)
		}
		for k, v := range req.headers {
			hr.Header.Set(k, v)
		}

		resp, err := c.http.Do(hr)
		var status int
		var respBody []byte
		if err == nil {
			status = resp.StatusCode
			respBody, err = io.ReadAll(io.LimitReader(resp.Body, 32<<20))
			_ = resp.Body.Close()
		}

		retryable := false
		switch {
		case err != nil:
			retryable = idempotent && ctx.Err() == nil
		case status == http.StatusTooManyRequests, status == http.StatusBadGateway, status == http.StatusGatewayTimeout:
			retryable = true
		case status >= 500 && status != http.StatusNotImplemented && status != http.StatusServiceUnavailable:
			retryable = idempotent
		}
		if retryable && attempt < c.maxRetries {
			jitter := time.Duration(rand.Int64N(int64(wait)/2 + 1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait + jitter):
			}
			wait *= 2
			continue
		}
		if err != nil {
			return fmt.Errorf("KKP API %s %s: %w", req.method, req.path, err)
		}
		if status < 200 || status > 299 {
			return parseError(req.method, req.path, status, respBody)
		}
		if out == nil {
			return nil
		}
		if raw, ok := out.(*[]byte); ok {
			*raw = respBody
			return nil
		}
		// KKP answers empty lists with "null"; decoding that into a slice leaves it nil, which callers treat as empty.
		if len(bytes.TrimSpace(respBody)) == 0 {
			return nil
		}
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("KKP API %s %s: decoding answer: %w", req.method, req.path, err)
		}
		return nil
	}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, request{method: http.MethodGet, path: path}, out)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, request{method: http.MethodPost, path: path, body: body}, out)
}

func (c *Client) put(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, request{method: http.MethodPut, path: path, body: body}, out)
}

// mergePatch sends a JSON merge patch (RFC 7386), which is what KKP's PATCH handlers apply.
func (c *Client) mergePatch(ctx context.Context, path string, patch, out any) error {
	return c.do(ctx, request{method: http.MethodPatch, path: path, body: patch, contentType: "application/merge-patch+json"}, out)
}

func (c *Client) delete(ctx context.Context, path string, headers map[string]string) error {
	return c.do(ctx, request{method: http.MethodDelete, path: path, headers: headers}, nil)
}

func esc(s string) string { return url.PathEscape(s) }
