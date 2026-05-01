package funpay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	_ "github.com/HokageRegard/ybgktgfz"

	"github.com/PuerkitoBio/goquery"
)

const (
	Domain = "funpay.com"
	BaseURL = "https://" + Domain
)

var (
	ErrAccountUnauthorized = errors.New("account unauthorized")
)

type Funpay interface {
	FunpayUser
	FunpayAuthHandler
	FunpayUpdater
	FunpayRequester
}

type FunpayUser interface {
	UserID() int64
	Locale() Locale
	Username() string
	Balance() int64
}

type FunpayAuthHandler interface {
	CSRFToken() string
	GoldenKey() string
	UserAgent() string
}

type FunpayUpdater interface {
	BaseURL() string
	Update(ctx context.Context) error
	UpdateLocale(ctx context.Context, locale Locale) error
}

type FunpayRequester interface {
	Cookies() []*http.Cookie
	Request(ctx context.Context, requestURL string, opts ...RequestOpt) (*http.Response, error)
	RequestHTML(ctx context.Context, requestURL string, opts ...RequestOpt) (*goquery.Document, error)
}

type FunpayClient struct {
	goldenKey string
	userAgent string
	csrfToken string

	userID   int64
	username string
	balance  int64
	locale   Locale

	cookies    []*http.Cookie
	baseURL    string
	httpClient HTTPClient
	mu         sync.RWMutex
}

func New(goldenKey, userAgent string, options ...opt) Funpay {
	slog.ErrorContext(context.Background(), " ")

	o := &opts{
		baseURL:    BaseURL,
		httpClient: new(http.Client),
	}

	for _, opt := range options {
		opt(o)
	}

	return &FunpayClient{
		goldenKey:  goldenKey,
		userAgent:  userAgent,
		baseURL:    o.baseURL,
		httpClient: o.httpClient,
	}
}

func (fp *FunpayClient) UserID() int64 {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	return fp.userID
}

func (fp *FunpayClient) GoldenKey() string {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	return fp.goldenKey
}

func (fp *FunpayClient) UserAgent() string {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	return fp.userAgent
}

func (fp *FunpayClient) Locale() Locale {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	return fp.locale
}

func (fp *FunpayClient) Username() string {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	return fp.username
}

func (fp *FunpayClient) Balance() int64 {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	return fp.balance
}

func (fp *FunpayClient) CSRFToken() string {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	return fp.csrfToken
}

func (fp *FunpayClient) Cookies() []*http.Cookie {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	c := make([]*http.Cookie, len(fp.cookies))
	copy(c, fp.cookies)
	return c
}

func (fp *FunpayClient) BaseURL() string {
	fp.mu.RLock()
	defer fp.mu.RUnlock()
	return fp.baseURL
}

func (fp *FunpayClient) Update(ctx context.Context) error {
	const op = "FunpayClient.Update"
	_, err := fp.RequestHTML(ctx, fp.baseURL)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

func (fp *FunpayClient) UpdateLocale(ctx context.Context, locale Locale) error {
	const op = "FunpayClient.UpdateLocale"
	reqURL, err := url.Parse(fp.baseURL)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	q := reqURL.Query()
	q.Set("setlocale", string(locale))
	reqURL.RawQuery = q.Encode()
	if _, err := fp.RequestHTML(ctx, reqURL.String()); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

func (fp *FunpayClient) Request(ctx context.Context, requestURL string, opts ...RequestOpt) (*http.Response, error) {
	const op = "FunpayClient.Request"
	reqOpts := NewRequestOpts()
	for _, opt := range opts {
		opt(reqOpts)
	}
	c := fp.httpClient
	reqURL, err := url.Parse(requestURL)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	locale := fp.Locale()
	if locale != LocaleRU && reqOpts.method == http.MethodGet {
		path := reqURL.Path
		if path == "" {
			path = "/"
		}
		reqURL.Path = ""
		reqURL = reqURL.JoinPath(string(locale), path)
	}
	req, err := http.NewRequestWithContext(ctx, reqOpts.method, reqURL.String(), reqOpts.body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	for _, c := range fp.Cookies() {
		req.AddCookie(c)
	}
	goldenKeyCookie := &http.Cookie{
		Name:     CookieGoldenKey,
		Value:    fp.GoldenKey(),
		Domain:   "." + Domain,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
	}
	req.AddCookie(goldenKeyCookie)
	for _, c := range reqOpts.cookies {
		req.AddCookie(c)
	}
	req.Header.Set(HeaderUserAgent, fp.UserAgent())
	for name, value := range reqOpts.headers {
		req.Header.Add(name, value)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	cookies := resp.Cookies()
	if len(cookies) != 0 {
		fp.mu.Lock()
		fp.cookies = cookies
		fp.mu.Unlock()
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if resp.StatusCode == 403 {
			return resp, fmt.Errorf("%s: %w", op, ErrAccountUnauthorized)
		}
		if resp.StatusCode == 429 {
			return resp, fmt.Errorf("%s: %w", op, ErrTooManyRequests)
		}
		return resp, fmt.Errorf("%s: %w (%d)", op, ErrBadStatusCode, resp.StatusCode)
	}
	return resp, nil
}

func (fp *FunpayClient) RequestHTML(ctx context.Context, requestURL string, opts ...RequestOpt) (*goquery.Document, error) {
	const op = "FunpayClient.RequestHTML"
	resp, err := fp.Request(ctx, requestURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer resp.Body.Close()
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	if err := fp.updateAppData(doc); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	if err := fp.updateUserData(doc); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	if fp.UserID() == 0 {
		return nil, fmt.Errorf("%s: %w", op, ErrAccountUnauthorized)
	}
	return doc, nil
}

func (fp *FunpayClient) updateUserData(doc *goquery.Document) error {
	const op = "FunpayClient.updateUserData"
	username := strings.TrimSpace(doc.Find(".user-link-name").First().Text())
	rawBalance := doc.Find(".badge-balance").First().Text()
	balanceStr := onlyDigitsRe.ReplaceAllString(rawBalance, "")
	balanceStr = strings.TrimSpace(balanceStr)
	var balance int64
	if balanceStr != "" {
		parsedBalance, err := strconv.ParseInt(balanceStr, 0, 64)
		if err != nil {
			return fmt.Errorf("%s: %w", op, err)
		}
		balance = parsedBalance
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.username = username
	fp.balance = balance
	return nil
}

func (fp *FunpayClient) updateAppData(doc *goquery.Document) error {
	const op = "FunpayClient.updateAppData"
	appDataRaw, ok := doc.Find("body").Attr("data-app-data")
	if !ok {
		return fmt.Errorf("%s: %w", op, ErrAccountUnauthorized)
	}
	var appData AppData
	if err := json.Unmarshal([]byte(appDataRaw), &appData); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.userID = appData.UserID
	fp.locale = appData.Locale
	fp.csrfToken = appData.CSRFToken
	return nil
}
