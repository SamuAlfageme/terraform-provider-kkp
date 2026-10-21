package client_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/client"
	"github.com/SamuAlfageme/terraform-provider-kkp/internal/kkpmock"
)

func newClient(t *testing.T, host, token string, retries int) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{Host: host, Token: token, MaxRetries: retries})
	if err != nil {
		t.Fatal(err)
	}
	c.SetRetryWait(time.Millisecond)
	return c
}

func TestNewValidates(t *testing.T) {
	for _, cfg := range []client.Config{
		{Token: "t"},
		{Host: "kkp.example.com", Token: "t"},
		{Host: "ftp://kkp.example.com", Token: "t"},
		{Host: "https://kkp.example.com"},
		{Host: "https://kkp.example.com", Token: "t", CACertPEM: "not pem"},
	} {
		if _, err := client.New(cfg); err == nil {
			t.Errorf("New(%+v) succeeded, want an error", cfg)
		}
	}
	c, err := client.New(client.Config{Host: "https://kkp.example.com/api/", Token: "t"})
	if err != nil || c.Host() != "https://kkp.example.com" {
		t.Fatalf("got %v, %v; want the /api suffix dropped", c, err)
	}
}

func TestRetriesGatewayErrors(t *testing.T) {
	s := kkpmock.New()
	defer s.Close()
	s.FailNext = 2
	if _, err := newClient(t, s.URL, kkpmock.AdminToken, 3).ListProjects(context.Background()); err != nil {
		t.Fatalf("expected the client to retry past two 502s: %v", err)
	}
	s.FailNext = 5
	if _, err := newClient(t, s.URL, kkpmock.AdminToken, 1).ListProjects(context.Background()); err == nil {
		t.Fatal("expected an error after the retries ran out")
	}
}

func TestDoesNotRetryNonIdempotentServerErrors(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":500,"message":"boom","details":["one","two"]}}`))
	}))
	defer srv.Close()
	_, err := newClient(t, srv.URL, "t", 3).CreateProject(context.Background(), client.CreateProjectBody{Name: "x"})
	if calls != 1 {
		t.Errorf("POST was sent %d times, want 1", calls)
	}
	if err == nil || err.Error() != "KKP API POST /api/v1/projects: 500 boom (one; two)" {
		t.Errorf("unexpected error %v", err)
	}
}

func TestNullListsAndNotFound(t *testing.T) {
	s := kkpmock.New()
	defer s.Close()
	c := newClient(t, s.URL, kkpmock.AdminToken, 0)
	ctx := context.Background()
	p := s.AddProject("p")
	keys, err := c.ListSSHKeys(ctx, p)
	if err != nil || keys != nil {
		t.Fatalf("got %v, %v; want a nil list for KKP's null", keys, err)
	}
	if _, err := c.GetSSHKey(ctx, p, "nope"); !client.IsNotFound(err) {
		t.Fatalf("got %v, want not found", err)
	}
	if _, err := c.GetProject(ctx, "nope"); !client.IsNotFound(err) {
		t.Fatalf("got %v, want not found", err)
	}
}

func TestTokenProjectID(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	jwt := enc([]byte(`{"alg":"HS256"}`)) + "." + enc([]byte(`{"email":"sa@x","project_id":"abc123","token_id":"t"}`)) + ".sig"
	for token, want := range map[string]string{jwt: "abc123", "opaque": "", "a.b.c": "", "": ""} {
		if got := client.TokenProjectID(token); got != want {
			t.Errorf("TokenProjectID(%q) = %q, want %q", token, got, want)
		}
	}
}

func TestServiceAccountScope(t *testing.T) {
	s := kkpmock.New()
	defer s.Close()
	mine, other := s.AddProject("mine"), s.AddProject("other")
	c := newClient(t, s.URL, s.ServiceAccountToken(mine), 0)
	if c.TokenProjectID() != mine {
		t.Fatalf("token project = %q, want %q", c.TokenProjectID(), mine)
	}
	ps, err := c.ListProjects(context.Background())
	if err != nil || len(ps) != 1 || ps[0].ID != mine {
		t.Fatalf("got %v, %v; want only the token's project", ps, err)
	}
	if _, err := c.GetProject(context.Background(), other); !client.IsForbidden(err) {
		t.Fatalf("got %v, want forbidden", err)
	}
}
