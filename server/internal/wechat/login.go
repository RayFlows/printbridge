package wechat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/RayFlows/printbridge/server/internal/accounts"
)

type Login struct {
	appID, secret string
	client        *http.Client
}

func NewLogin(appID, secret string, transport http.RoundTripper) *Login {
	return &Login{appID: appID, secret: secret, client: &http.Client{
		Timeout: 8 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (l *Login) OpenID(ctx context.Context, code string) (string, error) {
	query := url.Values{"appid": {l.appID}, "secret": {l.secret},
		"js_code": {code}, "grant_type": {"authorization_code"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.weixin.qq.com/sns/jscode2session?"+query.Encode(), nil)
	if err != nil {
		return "", accounts.ErrUnavailable
	}
	response, err := l.client.Do(req)
	if err != nil {
		// Transport errors can contain the URL, including AppSecret and login code.
		return "", accounts.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", accounts.ErrUnavailable
	}
	var result struct {
		OpenID string `json:"openid"`
		Code   int    `json:"errcode"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8192))
	if err := decoder.Decode(&result); err != nil {
		return "", accounts.ErrUnavailable
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", accounts.ErrUnavailable
	}
	if result.Code == -1 || result.Code == 45011 {
		return "", accounts.ErrUnavailable
	}
	if result.Code != 0 || result.OpenID == "" || len(result.OpenID) > 128 {
		return "", accounts.ErrLogin
	}
	return result.OpenID, nil
}
