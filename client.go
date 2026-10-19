// Package oddsrelay is the OddsRelay Go client: one method for each GET route of the OddsRelay API
// (https://oddsrelay.io/docs), which serves bookmaker and exchange odds for the UK & Ireland as JSON.
//
// The key comes from NewClient or the ODDSRELAY_KEY environment variable and travels only in the
// Authorization header. Every request asks for gzip, sends back the URL's last ETag so an unchanged
// reply is a free 304, and waits out a 429's Retry-After, three requests in all.
package oddsrelay

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// Version is this client's version, sent in the User-Agent header.
	Version = "0.1.0"
	// ContractVersion is the info.version of the API description these methods follow.
	ContractVersion = "2026-07-08"
	// DefaultBaseURL is where the API is served.
	DefaultBaseURL = "https://api.oddsrelay.io"
)

const (
	maxTries  = 3                // a 429 is retried until this many requests have been made
	maxWait   = 60 * time.Second // a longer Retry-After is returned to the caller, not slept through
	cacheSize = 32               // URLs whose last ETag and body are kept for conditional requests
)

// Client calls the OddsRelay API. It is safe for concurrent use.
type Client struct {
	key     string
	baseURL string
	http    *http.Client
	sleep   func(context.Context, time.Duration) error

	mu    sync.Mutex
	cache map[string]cached
	order []string // cache keys, oldest stored first
}

type cached struct {
	etag string
	body []byte
}

// Option changes a Client made by NewClient.
type Option func(*Client)

// WithBaseURL sends requests to another address, such as a test server.
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient sends requests through h, for your own timeout or transport. The default
// client times out each request after half a minute.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// NewClient returns a client for the OddsRelay API. An empty key is read from the ODDSRELAY_KEY
// environment variable. Status, Coverage, OpenAPI and Healthz answer without one.
func NewClient(key string, opts ...Option) (*Client, error) {
	if key == "" {
		key = os.Getenv("ODDSRELAY_KEY")
	}
	key = strings.TrimSpace(key) // a key read from a file often ends in a newline
	for i := 0; i < len(key); i++ {
		if key[i] < '!' || key[i] > '~' { // the message never echoes the key
			return nil, errors.New("oddsrelay: the API key holds characters an HTTP header cannot carry: copy it again")
		}
	}
	c := &Client{
		key:     key,
		baseURL: DefaultBaseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
		sleep:   sleep,
		cache:   map[string]cached{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// String says whether a key is set, never the key itself.
func (c *Client) String() string {
	if c.key == "" {
		return "oddsrelay.Client(key not set)"
	}
	return "oddsrelay.Client(key set)"
}

// GoString keeps the key out of %#v.
func (c *Client) GoString() string { return c.String() }

// Bool returns a pointer to v, for the *bool fields of the params structs.
func Bool(v bool) *bool { return &v }

// Amount is a token count from the API, which can also read "unlimited" or be left out.
type Amount struct {
	Value     int64
	Unlimited bool
	Known     bool // false when the header or field was absent or null
}

func (a Amount) String() string {
	switch {
	case a.Unlimited:
		return "unlimited"
	case !a.Known:
		return "unknown"
	}
	return strconv.FormatInt(a.Value, 10)
}

// MarshalJSON writes a number, "unlimited" or null, as UnmarshalJSON reads them.
func (a Amount) MarshalJSON() ([]byte, error) {
	switch {
	case a.Unlimited:
		return []byte(`"unlimited"`), nil
	case !a.Known:
		return []byte("null"), nil
	}
	return strconv.AppendInt(nil, a.Value, 10), nil
}

// UnmarshalJSON reads a number, "unlimited" or null.
func (a *Amount) UnmarshalJSON(b []byte) error {
	*a = amount(strings.Trim(string(b), `"`))
	return nil
}

func amount(s string) Amount {
	if s == "unlimited" {
		return Amount{Unlimited: true, Known: true}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return Amount{Value: n, Known: err == nil}
}

// Tokens holds the X-Tokens-* headers: what the call cost and where the balance stands.
type Tokens struct {
	Cost, Used, Remaining, Limit Amount
	Reset                        string // when the balance resets, ISO 8601; "" when absent
}

// RateLimit holds the X-RateLimit-* headers, for whichever window has fewer requests left.
type RateLimit struct {
	Limit, Remaining Amount
	Reset            Amount // seconds until the window ends
}

// Response is what every reply carries besides its decoded data.
type Response struct {
	StatusCode int
	Header     http.Header
	// Body is the decompressed body with every field kept, including fields added after this release.
	Body []byte
	// NotModified is true on a 304: nothing changed and nothing was spent. Body (and a Result's Data)
	// is then the last reply the client kept for this URL.
	NotModified bool
	ETag        string
	RequestID   string // quote it when you contact us
	Tokens      Tokens
	RateLimit   RateLimit
}

// Result is a successful reply, with its body decoded into Data.
type Result[T any] struct {
	Data T
	Response
}

// APIError is a reply the API refused. Branch on Code, never on Message.
type APIError struct {
	Code    string // "" on the front door's empty-bodied 429 and 502
	Message string
	Valid   []string // the accepted values, where the API lists them
	// A 402 (insufficient_tokens) carries the call's cost, the balance left and when it resets.
	Cost      int64
	Remaining Amount
	ResetsAt  string
	// RetryAfter is the reply's Retry-After, on a 429 and a 503.
	RetryAfter time.Duration
	Response   // Body holds the whole error object, unknown fields kept
}

func (e *APIError) Error() string {
	s := "oddsrelay: " + e.Message
	if e.Code != "" {
		s = fmt.Sprintf("oddsrelay: %d %s: %s", e.StatusCode, e.Code, e.Message)
	}
	if e.RequestID != "" {
		s += " (request_id " + e.RequestID + ")"
	}
	return s
}

// QuoteTooHighError is returned when a call's quote was above its MaxTokens. The call was not
// made and nothing was spent.
type QuoteTooHighError struct {
	Cost, MaxTokens int64
	Quote           *Result[Quote]
}

func (e *QuoteTooHighError) Error() string {
	return fmt.Sprintf("oddsrelay: this call costs %d tokens, above MaxTokens %d: add filters to narrow it", e.Cost, e.MaxTokens)
}

// OddsParams filters a matched board. Every field is optional, and a zero value is not sent.
type OddsParams struct {
	Region           string    `query:"region"`
	OddsFormat       string    `query:"oddsFormat"` // "decimal" (the API's default) or "american"
	Sports           []string  `query:"sports"`
	Bookmakers       []string  `query:"bookmakers"`
	Exchanges        []string  `query:"exchanges"`
	Markets          []string  `query:"markets"`
	CommenceTimeFrom time.Time `query:"commenceTimeFrom"`
	CommenceTimeTo   time.Time `query:"commenceTimeTo"`
	// MaxTokens, above 0, quotes the call first and returns a *QuoteTooHighError, without
	// making it, when it would cost more. It is not sent.
	MaxTokens int64
}

// RawParams filters the raw board. Every field is optional, and a zero value is not sent.
type RawParams struct {
	Region            string    `query:"region"`
	OddsFormat        string    `query:"oddsFormat"`
	Sports            []string  `query:"sports"`
	Sport             string    `query:"sport"`
	Leagues           []string  `query:"leagues"`
	Bookmakers        []string  `query:"bookmakers"`
	RequireBookmakers []string  `query:"requireBookmakers"`
	Markets           []string  `query:"markets"`
	EventIDs          []string  `query:"eventIds"`
	CommenceTimeFrom  time.Time `query:"commenceTimeFrom"`
	CommenceTimeTo    time.Time `query:"commenceTimeTo"`
	UpdatedSince      time.Time `query:"updatedSince"`
	ExchangesOnly     *bool     `query:"exchangesOnly"`
	IncludeLinks      *bool     `query:"includeLinks"`
	IncludeLiquidity  *bool     `query:"includeLiquidity"`
	Cursor            string    `query:"cursor"` // a page's Meta.NextCursor, for the page after it
	Limit             int       `query:"limit"`  // events per page, 1 to 250
	MaxTokens         int64     // as in OddsParams; with RawPages it caps each page
}

// EventParams filters one event's raw prices.
type EventParams struct {
	Region           string `query:"region"`
	OddsFormat       string `query:"oddsFormat"`
	IncludeLinks     *bool  `query:"includeLinks"`
	IncludeLiquidity *bool  `query:"includeLiquidity"`
	MaxTokens        int64  // as in OddsParams
}

// SportsParams filters the sports list.
type SportsParams struct {
	Region string `query:"region"`
	Cursor string `query:"cursor"`
}

// EventsParams filters the events of a sport.
type EventsParams struct {
	Region           string    `query:"region"`
	Cursor           string    `query:"cursor"`
	EventIDs         []string  `query:"eventIds"`
	CommenceTimeFrom time.Time `query:"commenceTimeFrom"`
	CommenceTimeTo   time.Time `query:"commenceTimeTo"`
}

// Odds gets one matched board: "standard", "2up", "dutching", "each-way", "extra-place" or "bog".
func (c *Client) Odds(ctx context.Context, board string, p OddsParams) (*Result[MatchedBoard], error) {
	return priced[MatchedBoard](ctx, c, "/v2/odds/"+url.PathEscape(board), query(p), p.MaxTokens)
}

// QuoteOdds prices an Odds call without making it. A quote spends no tokens.
func (c *Client) QuoteOdds(ctx context.Context, board string, p OddsParams) (*Result[Quote], error) {
	return c.quote(ctx, "/v2/odds/"+url.PathEscape(board), query(p))
}

// Raw gets one page of the raw board: bookmaker and exchange prices by event, unpaired.
func (c *Client) Raw(ctx context.Context, p RawParams) (*Result[RawPage], error) {
	return priced[RawPage](ctx, c, "/v2/odds/raw", query(p), p.MaxTokens)
}

// QuoteRaw prices a Raw call without making it. A quote spends no tokens.
func (c *Client) QuoteRaw(ctx context.Context, p RawParams) (*Result[Quote], error) {
	return c.quote(ctx, "/v2/odds/raw", query(p))
}

// RawPages calls fn with each page of the raw board in turn, from p.Cursor (or the first page) to
// the last, following Meta.NextCursor. It returns the first error, from the API or from fn.
func (c *Client) RawPages(ctx context.Context, p RawParams, fn func(*Result[RawPage]) error) error {
	for {
		page, err := c.Raw(ctx, p)
		if err != nil {
			return err
		}
		if err := fn(page); err != nil {
			return err
		}
		if page.Data.Meta.NextCursor == "" {
			return nil
		}
		p.Cursor = page.Data.Meta.NextCursor
	}
}

// Event gets one event's raw prices.
func (c *Client) Event(ctx context.Context, id string, p EventParams) (*Result[RawEvent], error) {
	return priced[RawEvent](ctx, c, "/v2/odds/event/"+url.PathEscape(id), query(p), p.MaxTokens)
}

// QuoteEvent prices an Event call without making it. A quote spends no tokens.
func (c *Client) QuoteEvent(ctx context.Context, id string, p EventParams) (*Result[Quote], error) {
	return c.quote(ctx, "/v2/odds/event/"+url.PathEscape(id), query(p))
}

// Sports gets a page of sport and competition keys.
func (c *Client) Sports(ctx context.Context, p SportsParams) (*Result[SportsPage], error) {
	return get[SportsPage](ctx, c, "/v2/sports", query(p), true)
}

// Events gets a page of the events of one sport key.
func (c *Client) Events(ctx context.Context, sport string, p EventsParams) (*Result[EventsPage], error) {
	q := query(p)
	q.Set("sport", sport)
	return get[EventsPage](ctx, c, "/v2/events", q, true)
}

// Bookmakers gets the venues you can filter on, with their keys.
func (c *Client) Bookmakers(ctx context.Context) (*Result[Venues], error) {
	return get[Venues](ctx, c, "/v2/bookmakers", nil, true)
}

// Regions gets the region codes open to your key.
func (c *Client) Regions(ctx context.Context) (*Result[Regions], error) {
	return get[Regions](ctx, c, "/v2/regions", nil, true)
}

// Coverage gets which venues and sports each product carries, for region ("" for the API's
// default). It answers without a key; with one the reply has more detail, in another shape,
// so Data is the JSON as sent.
func (c *Client) Coverage(ctx context.Context, region string) (*Result[json.RawMessage], error) {
	return get[json.RawMessage](ctx, c, "/v2/coverage", regionQuery(region), true)
}

// Status gets whether each product is serving, for region ("" for the API's default). Keyless.
func (c *Client) Status(ctx context.Context, region string) (*Result[Status], error) {
	return get[Status](ctx, c, "/v2/status", regionQuery(region), false)
}

// Health gets when each board was last processed, and whether any is behind.
func (c *Client) Health(ctx context.Context) (*Result[Health], error) {
	return get[Health](ctx, c, "/v2/health", nil, true)
}

// Usage gets your plan, tokens used and left, and what your key may read.
func (c *Client) Usage(ctx context.Context) (*Result[Usage], error) {
	return get[Usage](ctx, c, "/v2/usage", nil, true)
}

// Pricing gets the token rate card.
func (c *Client) Pricing(ctx context.Context) (*Result[PriceTable], error) {
	return get[PriceTable](ctx, c, "/v2/pricing", nil, true)
}

// OpenAPI gets the API description, as JSON. Keyless.
func (c *Client) OpenAPI(ctx context.Context) (*Result[json.RawMessage], error) {
	return get[json.RawMessage](ctx, c, "/v2/openapi.json", nil, false)
}

// Healthz gets the plain-text liveness check. Keyless.
func (c *Client) Healthz(ctx context.Context) (*Result[string], error) {
	return get[string](ctx, c, "/healthz", nil, false)
}

func regionQuery(region string) url.Values {
	if region == "" {
		return nil
	}
	return url.Values{"region": {region}}
}

// query turns a params struct into query parameters by its fields' query tags. Zero values are left
// out, lists are joined with commas and times are sent in UTC.
func query(p any) url.Values {
	q := url.Values{}
	v := reflect.ValueOf(p)
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Tag.Get("query")
		if name == "" {
			continue
		}
		switch f := v.Field(i).Interface().(type) {
		case string:
			if f != "" {
				q.Set(name, f)
			}
		case []string:
			if len(f) > 0 {
				q.Set(name, strings.Join(f, ","))
			}
		case *bool:
			if f != nil {
				q.Set(name, strconv.FormatBool(*f))
			}
		case int:
			if f != 0 {
				q.Set(name, strconv.Itoa(f))
			}
		case time.Time:
			if !f.IsZero() {
				q.Set(name, f.UTC().Format("2006-01-02T15:04:05Z"))
			}
		}
	}
	return q
}

func (c *Client) quote(ctx context.Context, path string, q url.Values) (*Result[Quote], error) {
	q.Set("quote", "true")
	return get[Quote](ctx, c, path, q, true)
}

func priced[T any](ctx context.Context, c *Client, path string, q url.Values, maxTokens int64) (*Result[T], error) {
	if maxTokens > 0 {
		withQuote := url.Values{}
		for k, v := range q {
			withQuote[k] = v
		}
		quote, err := c.quote(ctx, path, withQuote)
		if err != nil {
			return nil, err
		}
		if quote.Data.Cost > maxTokens {
			return nil, &QuoteTooHighError{Cost: quote.Data.Cost, MaxTokens: maxTokens, Quote: quote}
		}
	}
	return get[T](ctx, c, path, q, true)
}

func get[T any](ctx context.Context, c *Client, path string, q url.Values, sendKey bool) (*Result[T], error) {
	r, err := c.do(ctx, path, q, sendKey)
	if err != nil {
		return nil, err
	}
	res := &Result[T]{Response: *r}
	if s, ok := any(&res.Data).(*string); ok {
		*s = string(r.Body)
	} else if err := json.Unmarshal(r.Body, &res.Data); err != nil {
		return nil, fmt.Errorf("oddsrelay: reading the reply to %s: %w", path, err)
	}
	return res, nil
}

func (c *Client) do(ctx context.Context, path string, q url.Values, sendKey bool) (*Response, error) {
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	c.mu.Lock()
	prev, hit := c.cache[u]
	c.mu.Unlock()

	var res *http.Response
	var body []byte
	for try := 1; ; try++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		// Asking for gzip ourselves turns off net/http's own decompression, so read does it.
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("User-Agent", "oddsrelay-go/"+Version)
		if sendKey && c.key != "" {
			req.Header.Set("Authorization", "Bearer "+c.key)
		}
		if hit {
			req.Header.Set("If-None-Match", prev.etag)
		}
		if res, err = c.http.Do(req); err != nil {
			return nil, err
		}
		if body, err = read(res); err != nil {
			return nil, err
		}
		wait := retryAfter(res.Header)
		if res.StatusCode != http.StatusTooManyRequests || try == maxTries || wait > maxWait {
			break
		}
		if err := c.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}

	h := res.Header
	r := &Response{
		StatusCode: res.StatusCode,
		Header:     h,
		Body:       body,
		ETag:       h.Get("ETag"),
		RequestID:  h.Get("X-OddsRelay-Request-Id"),
		Tokens: Tokens{
			Cost:      amount(h.Get("X-Tokens-Cost")),
			Used:      amount(h.Get("X-Tokens-Used")),
			Remaining: amount(h.Get("X-Tokens-Remaining")),
			Limit:     amount(h.Get("X-Tokens-Limit")),
			Reset:     h.Get("X-Tokens-Reset"),
		},
		RateLimit: RateLimit{
			Limit:     amount(h.Get("X-RateLimit-Limit")),
			Remaining: amount(h.Get("X-RateLimit-Remaining")),
			Reset:     amount(h.Get("X-RateLimit-Reset")),
		},
	}
	switch {
	case res.StatusCode == http.StatusNotModified && hit:
		r.NotModified, r.Body = true, bytes.Clone(prev.body) // a caller's edits never reach the cache
	case res.StatusCode >= 300:
		return nil, c.apiError(r)
	case r.ETag != "":
		c.remember(u, cached{r.ETag, bytes.Clone(body)})
	}
	return r, nil
}

func (c *Client) remember(u string, e cached) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.cache[u]; !ok {
		c.order = append(c.order, u)
	}
	c.cache[u] = e
	if len(c.order) > cacheSize { // ponytail: drops the oldest stored URL, not the least used
		delete(c.cache, c.order[0])
		c.order = c.order[1:]
	}
}

func read(res *http.Response) ([]byte, error) {
	defer res.Body.Close()
	var r io.Reader = res.Body
	if res.Header.Get("Content-Encoding") == "gzip" {
		// Header then describes the decompressed Body, as net/http's own decompression leaves it.
		res.Header.Del("Content-Encoding")
		res.Header.Del("Content-Length")
		zr, err := gzip.NewReader(res.Body)
		if err == io.EOF { // no body, as on a 304
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		r = zr
	}
	return io.ReadAll(r)
}

func retryAfter(h http.Header) time.Duration {
	n, err := strconv.Atoi(h.Get("Retry-After"))
	if err != nil || n < 0 {
		n = 1
	}
	return time.Duration(n) * time.Second
}

func (c *Client) apiError(r *Response) *APIError {
	if c.key != "" { // a key never reaches an error, even if a message ever echoed one
		r.Body = bytes.ReplaceAll(r.Body, []byte(c.key), []byte("***"))
	}
	var b struct {
		Error struct {
			Code      string   `json:"code"`
			Message   string   `json:"message"`
			RequestID string   `json:"request_id"`
			Valid     []string `json:"valid"`
			Cost      int64    `json:"cost"`
			Remaining Amount   `json:"remaining"`
			ResetsAt  string   `json:"resets_at"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.Body, &b) // the front door's 429 and 502 have an empty body
	e := b.Error
	err := &APIError{Code: e.Code, Message: e.Message, Valid: e.Valid, Cost: e.Cost, Remaining: e.Remaining,
		ResetsAt: e.ResetsAt, Response: *r}
	if err.Message == "" {
		err.Message = "HTTP " + strconv.Itoa(r.StatusCode)
	}
	if e.RequestID != "" {
		err.RequestID = e.RequestID
	}
	if h := r.Header.Get("Retry-After"); h != "" {
		err.RetryAfter = retryAfter(r.Header)
	}
	return err
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
