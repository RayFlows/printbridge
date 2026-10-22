package wechat_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/RayFlows/printbridge/server/internal/accounts"
	"github.com/RayFlows/printbridge/server/internal/wechat"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLoginExchangeAndSanitizedFailures(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		err    error
	}{
		{`{"openid":"verified-user","session_key":"private-key"}`, 200, nil},
		{`{"errcode":40029,"errmsg":"private response"}`, 200, accounts.ErrLogin},
		{`{"errcode":-1}`, 200, accounts.ErrUnavailable},
		{`{"errcode":45011}`, 200, accounts.ErrUnavailable},
		{`{}`, 200, accounts.ErrLogin},
		{`null`, 200, accounts.ErrLogin},
		{`invalid json`, 200, accounts.ErrUnavailable},
		{`{"openid":"user"}{}`, 200, accounts.ErrUnavailable},
		{`{"openid":"user"}`, 302, accounts.ErrUnavailable},
		{`unavailable`, 503, accounts.ErrUnavailable},
	} {
		t.Run(tc.body, func(t *testing.T) {
			client := wechat.NewLogin("synthetic-app", "test-secret", transport(func(r *http.Request) (*http.Response, error) {
				q := r.URL.Query()
				if r.URL.Scheme != "https" || r.URL.Host != "api.weixin.qq.com" || r.URL.Path != "/sns/jscode2session" ||
					q.Get("appid") != "synthetic-app" || q.Get("secret") != "test-secret" || q.Get("js_code") != "one-use-code" ||
					q.Get("grant_type") != "authorization_code" {
					t.Fatal("wrong exchange request")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": {"https://redirect.invalid"}},
					Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			}))
			id, err := client.OpenID(context.Background(), "one-use-code")
			if !errors.Is(err, tc.err) {
				t.Fatal(err)
			}
			if tc.err == nil && id != "verified-user" {
				t.Fatal("missing verified identifier")
			}
			if err != nil && (strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "test-secret")) {
				t.Fatal("secret leak")
			}
		})
	}
	client := wechat.NewLogin("synthetic-app", "test-secret", transport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("https://private/?secret=test-secret")
	}))
	if _, err := client.OpenID(context.Background(), "code"); err != accounts.ErrUnavailable {
		t.Fatal("transport diagnostic escaped", err)
	}
}
