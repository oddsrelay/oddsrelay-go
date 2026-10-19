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
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fixtures in testdata are the API description's own response examples (links dropped from the
// matched boards). No test calls the API.

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type reply struct {
	status int
	header map[string]string
	body   []byte
	plain  bool // send the body as is, even when the request asks for gzip
}

type seen struct {
	path   string // escaped
	query  url.Values
	header http.Header
}

// fake is a test server that answers with its replies in order and records each request, plus a
// client pointed at it whose waits are recorded instead of slept.
type fake struct {
	*Client
	mu      sync.Mutex
	reqs    []seen
	waits   []time.Duration
	gzipped int // replies the server compressed
}

func serve(t *testing.T, key string, replies ...reply) *fake {
	t.Helper()
	f := &fake{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		i := len(f.reqs)
		f.reqs = append(f.reqs, seen{r.URL.EscapedPath(), r.URL.Query(), r.Header.Clone()})
		f.mu.Unlock()
		if i >= len(replies) {
			t.Errorf("unexpected request %d: %s", i+1, r.URL)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		rp := replies[i]
		for k, v := range rp.header {
			w.Header().Set(k, v)
		}
		body := rp.body
		if len(body) > 0 && !rp.plain && r.Header.Get("Accept-Encoding") == "gzip" {
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			zw.Write(body)
			zw.Close()
			body = buf.Bytes()
			w.Header().Set("Content-Encoding", "gzip")
			f.mu.Lock()
			f.gzipped++
			f.mu.Unlock()
		}
		if rp.status == 0 {
			rp.status = http.StatusOK
		}
		w.WriteHeader(rp.status)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(key, WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(_ context.Context, d time.Duration) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.waits = append(f.waits, d)
		return nil
	}
	f.Client = c
	return f
}

func (f *fake) requests() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seen(nil), f.reqs...)
}

func jsonReply(body []byte) reply {
	return reply{header: map[string]string{"Content-Type": "application/json"}, body: body}
}

var ctx = context.Background()

func TestKey(t *testing.T) {
	t.Setenv("ODDSRELAY_KEY", " env-key\n")

	f := serve(t, "", jsonReply(fixture(t, "v2-usage.usage.json")))
	if _, err := f.Usage(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.requests()[0].header.Get("Authorization"); got != "Bearer env-key" {
		t.Errorf("key from the environment: Authorization = %q", got)
	}
	if strings.Contains(f.requests()[0].query.Encode(), "env-key") {
		t.Error("the key reached the query string")
	}

	f = serve(t, "arg-key", jsonReply(fixture(t, "v2-usage.usage.json")))
	if _, err := f.Usage(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.requests()[0].header.Get("Authorization"); got != "Bearer arg-key" {
		t.Errorf("an argument wins over the environment: Authorization = %q", got)
	}
	for _, s := range []string{f.String(), fmt.Sprintf("%v %+v %#v", f.Client, f.Client, f.Client)} {
		if strings.Contains(s, "arg-key") {
			t.Errorf("printing the client shows the key: %s", s)
		}
	}

	_, err := NewClient("bad\nkey")
	if err == nil || strings.Contains(err.Error(), "bad") {
		t.Errorf("a key a header cannot carry: err = %v", err)
	}
}

func TestKeylessRoutesSendNoKey(t *testing.T) {
	f := serve(t, "test-key",
		jsonReply(fixture(t, "v2-status.status.json")),
		reply{body: fixture(t, "healthz.txt"), plain: true, header: map[string]string{"Content-Type": "text/plain"}},
		jsonReply([]byte(`{"openapi":"3.1.0","info":{"version":"2026-07-08"}}`)),
	)
	status, err := f.Status(ctx, "uk")
	if err != nil {
		t.Fatal(err)
	}
	if status.Data.Products["raw"] != "ok" {
		t.Errorf("status products = %v", status.Data.Products)
	}
	live, err := f.Healthz(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(live.Data, "ok\n") {
		t.Errorf("healthz = %q", live.Data)
	}
	if _, err := f.OpenAPI(ctx); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.requests() {
		if r.header.Get("Authorization") != "" {
			t.Errorf("%s was sent the key", r.path)
		}
	}
}

func TestQueryEncoding(t *testing.T) {
	quote := jsonReply(fixture(t, "v2-odds-type.quote.json"))
	f := serve(t, "test-key", quote, quote, quote, jsonReply(fixture(t, "v2-sports.page.json")))

	from := time.Date(2026, 10, 19, 13, 0, 0, 0, time.FixedZone("UTC+1", 3600))
	if _, err := f.QuoteOdds(ctx, "each-way", OddsParams{
		Region:           "uk",
		Sports:           []string{"horse_racing", "greyhounds"},
		CommenceTimeFrom: from,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.QuoteRaw(ctx, RawParams{IncludeLiquidity: Bool(false), Limit: 50, EventIDs: []string{"or_evt_1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.QuoteEvent(ctx, "a/b?c", EventParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Events(ctx, "tennis", EventsParams{Region: "uk"}); err != nil {
		t.Fatal(err)
	}

	want := []seen{
		{path: "/v2/odds/each-way", query: url.Values{"region": {"uk"}, "sports": {"horse_racing,greyhounds"},
			"commenceTimeFrom": {"2026-10-19T12:00:00Z"}, "quote": {"true"}}},
		{path: "/v2/odds/raw", query: url.Values{"includeLiquidity": {"false"}, "limit": {"50"},
			"eventIds": {"or_evt_1"}, "quote": {"true"}}},
		{path: "/v2/odds/event/a%2Fb%3Fc", query: url.Values{"quote": {"true"}}},
		{path: "/v2/events", query: url.Values{"sport": {"tennis"}, "region": {"uk"}}},
	}
	for i, r := range f.requests() {
		if r.path != want[i].path || !reflect.DeepEqual(r.query, want[i].query) {
			t.Errorf("request %d: %s?%s, want %s?%s", i+1, r.path, r.query.Encode(), want[i].path, want[i].query.Encode())
		}
	}
}

func TestGzipAndHeaders(t *testing.T) {
	f := serve(t, "test-key", reply{
		header: map[string]string{
			"Content-Type":           "application/json",
			"X-Tokens-Cost":          "0",
			"X-Tokens-Remaining":     "unlimited",
			"X-RateLimit-Remaining":  "29",
			"X-OddsRelay-Request-Id": "or_req_1",
		},
		body: fixture(t, "v2-sports.page.json"),
	})
	res, err := f.Sports(ctx, SportsParams{})
	if err != nil {
		t.Fatal(err)
	}
	r := f.requests()[0]
	if r.header.Get("Accept-Encoding") != "gzip" || r.header.Get("User-Agent") != "oddsrelay-go/"+Version {
		t.Errorf("request headers = %v", r.header)
	}
	if f.gzipped != 1 {
		t.Fatal("the test server did not gzip the reply")
	}
	if res.Header.Get("Content-Encoding") != "" || res.Header.Get("Content-Length") != "" {
		t.Errorf("Header still describes the compressed body: %v", res.Header)
	}
	if got := res.Data.Data[0].Key; got != "american_football_american_football" {
		t.Errorf("first sport = %q", got)
	}
	if res.Tokens.Cost.String() != "0" || !res.Tokens.Remaining.Unlimited || res.Tokens.Used.Known {
		t.Errorf("tokens = %+v", res.Tokens)
	}
	if res.RateLimit.Remaining.Value != 29 || res.RequestID != "or_req_1" {
		t.Errorf("rate limit %+v, request id %q", res.RateLimit, res.RequestID)
	}
}

func TestRawPagesToTheEnd(t *testing.T) {
	first := fixture(t, "v2-odds-raw.page.json")
	var page map[string]any
	if err := json.Unmarshal(first, &page); err != nil {
		t.Fatal(err)
	}
	cursor := page["meta"].(map[string]any)["next_cursor"].(string)
	page["meta"].(map[string]any)["next_cursor"] = nil
	last, _ := json.Marshal(page)

	f := serve(t, "test-key", jsonReply(first), jsonReply(last))
	var pages []*Result[RawPage]
	err := f.RawPages(ctx, RawParams{Region: "uk", Sports: []string{"tennis"}}, func(p *Result[RawPage]) error {
		pages = append(pages, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("%d pages, want 2", len(pages))
	}
	reqs := f.requests()
	if reqs[0].query.Has("cursor") || reqs[1].query.Get("cursor") != cursor || reqs[1].query.Get("sports") != "tennis" {
		t.Errorf("queries = %v, %v", reqs[0].query, reqs[1].query)
	}
	meta, event := pages[0].Data.Meta, pages[0].Data.Data[0]
	if meta.Total != 1312 || meta.BoardVersion != 1789611660384 || meta.NextCursor != cursor {
		t.Errorf("meta = %+v", meta)
	}
	if event.ID != "or_evt_da71d5873e85" || event.Bookmakers[0].Markets[0].Outcomes[0].Price != 1.7 {
		t.Errorf("first event = %+v", event)
	}

	f = serve(t, "test-key", jsonReply(first), reply{status: http.StatusPaymentRequired, body: fixture(t, "error-402.insufficient_tokens.json")})
	calls := 0
	err = f.RawPages(ctx, RawParams{}, func(*Result[RawPage]) error { calls++; return nil })
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 402 || calls != 1 {
		t.Errorf("an API error on page 2: err %v, fn called %d times", err, calls)
	}

	stop := errors.New("stop")
	f = serve(t, "test-key", jsonReply(first))
	if err := f.RawPages(ctx, RawParams{}, func(*Result[RawPage]) error { return stop }); err != stop {
		t.Errorf("an error from fn: got %v", err)
	}
}

func TestQuote(t *testing.T) {
	f := serve(t, "test-key", jsonReply(fixture(t, "v2-odds-type.quote.json")))
	q, err := f.QuoteOdds(ctx, "standard", OddsParams{Region: "uk"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Data.Cost != 0 || !q.Data.Remaining.Unlimited || q.Data.RateCard != "2026-09" {
		t.Errorf("quote = %+v", q.Data)
	}

	priced := jsonReply([]byte(`{"cost": 120, "remaining": 5000, "rate_card": "2026-09"}`))
	f = serve(t, "test-key", priced)
	_, err = f.Raw(ctx, RawParams{Sports: []string{"tennis"}, MaxTokens: 100})
	var high *QuoteTooHighError
	if !errors.As(err, &high) || high.Cost != 120 || high.MaxTokens != 100 || high.Quote.Data.Remaining.Value != 5000 {
		t.Fatalf("over the cap: err = %v", err)
	}
	if n := len(f.requests()); n != 1 {
		t.Errorf("over the cap made %d requests, want only the quote", n)
	}

	f = serve(t, "test-key", reply{status: http.StatusInternalServerError})
	_, err = f.Raw(ctx, RawParams{MaxTokens: 100})
	var e *APIError
	if !errors.As(err, &e) || len(f.requests()) != 1 {
		t.Errorf("a failed quote: err %v, %d requests, want only the quote", err, len(f.requests()))
	}

	f = serve(t, "test-key", priced, jsonReply(fixture(t, "v2-odds-type.standard_filtered.json")))
	board, err := f.Odds(ctx, "standard", OddsParams{Region: "uk", MaxTokens: 120})
	if err != nil {
		t.Fatal(err)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !reflect.DeepEqual(reqs[0].query, url.Values{"region": {"uk"}, "quote": {"true"}}) ||
		!reflect.DeepEqual(reqs[1].query, url.Values{"region": {"uk"}}) {
		t.Errorf("at the cap: requests = %v", reqs)
	}
	if board.Data.Meta.Count != 26 || board.Data.Data[0].Markets[0].Outcomes[0].Back[0].Price != 2.9 {
		t.Errorf("board = %+v", board.Data)
	}
}

func TestAmountJSON(t *testing.T) {
	for _, a := range []Amount{{Value: 5000, Known: true}, {Unlimited: true, Known: true}, {}} {
		var back Amount
		if b, err := json.Marshal(a); err != nil || json.Unmarshal(b, &back) != nil || back != a {
			t.Errorf("%v came back as %v (%s)", a, back, b)
		}
	}
}

func TestLayShapes(t *testing.T) {
	f := serve(t, "test-key",
		jsonReply(fixture(t, "v2-odds-type.standard_filtered.json")),
		jsonReply(fixture(t, "v2-odds-type.each_way.json")),
	)
	standard, err := f.Odds(ctx, "standard", OddsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var lays []LayOffer
	if err := json.Unmarshal(standard.Data.Data[0].Markets[0].Outcomes[0].Lay, &lays); err != nil || lays[0].Price != 3.45 ||
		lays[0].Available == nil || *lays[0].Available != 175 {
		t.Errorf("standard lay = %+v, %v", lays, err)
	}
	var unknown LayOffer
	if err := json.Unmarshal([]byte(`{"available":null}`), &unknown); err != nil || unknown.Available != nil {
		t.Errorf("null liquidity = %v, %v", unknown.Available, err)
	}
	eachWay, err := f.Odds(ctx, "each-way", OddsParams{})
	if err != nil {
		t.Fatal(err)
	}
	o := eachWay.Data.Data[0].Markets[0].Outcomes[0]
	var place PlaceLays
	if err := json.Unmarshal(o.Lay, &place); err != nil || place.Place[0].Places != 3 || o.Back[0].PlaceFraction != 0.2 {
		t.Errorf("each-way lay = %+v, %v", place, err)
	}
}

func TestAPIError(t *testing.T) {
	f := serve(t, "test-key", reply{
		status: http.StatusPaymentRequired,
		header: map[string]string{"Content-Type": "application/json", "X-Tokens-Cost": "0", "X-OddsRelay-Request-Id": "or_req_hdr"},
		body: []byte(strings.NewReplacer(`"cost": 0`, `"cost": 120`, `"remaining": 0`, `"remaining": 40`).
			Replace(string(fixture(t, "error-402.insufficient_tokens.json")))),
	})
	_, err := f.Odds(ctx, "dutching", OddsParams{})
	var e *APIError
	if !errors.As(err, &e) {
		t.Fatalf("err = %v", err)
	}
	if e.StatusCode != 402 || e.Code != "insufficient_tokens" || e.RequestID != "or_req_00000000000000000000000000000000" ||
		e.ResetsAt != "2026-10-01T00:00:00Z" || e.Cost != 120 || e.Remaining.Value != 40 || !e.Tokens.Cost.Known {
		t.Errorf("402 = %+v", e)
	}
	if !strings.HasPrefix(e.Error(), "oddsrelay: 402 insufficient_tokens: ") {
		t.Errorf("message = %q", e.Error())
	}

	f = serve(t, "test-key", reply{status: http.StatusBadRequest, body: fixture(t, "error-400.unknown_sport.json")})
	_, err = f.Odds(ctx, "standard", OddsParams{Sports: []string{"curling_xyz"}})
	if !errors.As(err, &e) || e.Code != "unknown_sport" || len(e.Valid) == 0 {
		t.Errorf("400 = %v", err)
	}

	f = serve(t, "test-key", reply{status: http.StatusBadGateway, header: map[string]string{"X-OddsRelay-Request-Id": "or_req_3"}})
	_, err = f.Health(ctx)
	if !errors.As(err, &e) || e.Code != "" || e.Error() != "oddsrelay: HTTP 502 (request_id or_req_3)" {
		t.Errorf("empty-bodied 502 = %v", err)
	}

	f = serve(t, "leaky-key", reply{status: http.StatusUnauthorized, body: []byte(`{"error":{"code":"invalid_api_key","message":"no leaky-key here"}}`)})
	_, err = f.Usage(ctx)
	if !errors.As(err, &e) || strings.Contains(err.Error(), "leaky-key") || bytes.Contains(e.Body, []byte("leaky-key")) {
		t.Errorf("the key reached an error: %v", err)
	}
}

func TestRetryAfter429(t *testing.T) {
	limited := func(after string) reply {
		h := map[string]string{"Content-Type": "application/json"}
		if after != "" {
			h["Retry-After"] = after
		}
		return reply{status: http.StatusTooManyRequests, header: h, body: fixture(t, "error-429.too_many_requests.json")}
	}

	f := serve(t, "test-key", limited("2"), jsonReply(fixture(t, "v2-usage.usage.json")))
	usage, err := f.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Data.Plan != "free" || !reflect.DeepEqual(f.waits, []time.Duration{2 * time.Second}) {
		t.Errorf("plan %q, waits %v", usage.Data.Plan, f.waits)
	}

	f = serve(t, "test-key", limited(""), limited(""), limited(""))
	_, err = f.Usage(ctx)
	var e *APIError
	if !errors.As(err, &e) || e.Code != "too_many_requests" || len(f.requests()) != 3 ||
		!reflect.DeepEqual(f.waits, []time.Duration{time.Second, time.Second}) {
		t.Errorf("three 429s: err %v, %d requests, waits %v", err, len(f.requests()), f.waits)
	}

	f = serve(t, "test-key", limited("120"))
	_, err = f.Usage(ctx)
	if !errors.As(err, &e) || e.RetryAfter != 2*time.Minute || len(f.waits) != 0 {
		t.Errorf("a wait over a minute: err %v, waits %v", err, f.waits)
	}
}

func TestNotModified(t *testing.T) {
	page := fixture(t, "v2-sports.page.json")
	f := serve(t, "test-key",
		reply{header: map[string]string{"Content-Type": "application/json", "ETag": `"c1a2b3c4d-334-gz"`}, body: page},
		reply{status: http.StatusNotModified, header: map[string]string{"Content-Encoding": "gzip"}},
		reply{status: http.StatusNotModified},
	)
	first, err := f.Sports(ctx, SportsParams{Region: "uk"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range first.Body { // a caller's edit must not reach the cached reply
		first.Body[i] = ' '
	}
	second, err := f.Sports(ctx, SportsParams{Region: "uk"})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.requests()[1].header.Get("If-None-Match"); got != `"c1a2b3c4d-334-gz"` {
		t.Errorf("If-None-Match = %q", got)
	}
	if first.NotModified || !second.NotModified || second.Data.Data[0].Key != first.Data.Data[0].Key || !bytes.Equal(second.Body, page) {
		t.Errorf("304: %+v", second.Response)
	}
	for i := range second.Body { // nor an edit to a 304's Body
		second.Body[i] = ' '
	}
	if third, err := f.Sports(ctx, SportsParams{Region: "uk"}); err != nil || !bytes.Equal(third.Body, page) {
		t.Errorf("a second 304: err %v", err)
	}
}

func TestRoutes(t *testing.T) {
	cases := []struct {
		path, file string
		key        bool
		call       func(*Client) (decoded bool, err error) // decoded: one field came through
	}{
		{"/v2/odds/event/or_evt_da71d5873e85", "v2-odds-event-id.event.json", true, func(c *Client) (bool, error) {
			r, err := c.Event(ctx, "or_evt_da71d5873e85", EventParams{})
			return err == nil && r.Data.ID == "or_evt_da71d5873e85" && len(r.Data.Bookmakers) > 0, err
		}},
		{"/v2/bookmakers", "v2-bookmakers.catalogue.json", true, func(c *Client) (bool, error) {
			r, err := c.Bookmakers(ctx)
			return err == nil && len(r.Data.Data) > 0 && r.Data.Data[0].Key != "", err
		}},
		{"/v2/regions", "v2-regions.regions.json", true, func(c *Client) (bool, error) {
			r, err := c.Regions(ctx)
			return err == nil && r.Data.DefaultRegion != "" && len(r.Data.Regions) > 0, err
		}},
		{"/v2/coverage", "v2-coverage.keyless.json", true, func(c *Client) (bool, error) {
			r, err := c.Coverage(ctx, "")
			return err == nil && bytes.Contains(r.Data, []byte(`"products"`)), err
		}},
		{"/v2/health", "v2-health.health.json", true, func(c *Client) (bool, error) {
			r, err := c.Health(ctx)
			return err == nil && r.Data.Status != "" && r.Data.Boards["raw"].AgeS != nil && *r.Data.Boards["raw"].AgeS == 2.2, err
		}},
		{"/v2/pricing", "v2-pricing.table.json", true, func(c *Client) (bool, error) {
			r, err := c.Pricing(ctx)
			return err == nil && r.Data.RateCard != "" && len(r.Data.Regions) > 0, err
		}},
	}
	for _, tc := range cases {
		f := serve(t, "test-key", jsonReply(fixture(t, tc.file)))
		if decoded, err := tc.call(f.Client); err != nil || !decoded {
			t.Errorf("%s: decoded %v, err %v", tc.path, decoded, err)
			continue
		}
		r := f.requests()[0]
		if r.path != tc.path || (r.header.Get("Authorization") != "") != tc.key {
			t.Errorf("%s: sent %s, key %v", tc.path, r.path, r.header.Get("Authorization") != "")
		}
	}
}

func TestContextCancellation(t *testing.T) {
	f := serve(t, "test-key")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.Usage(cancelled); !errors.Is(err, context.Canceled) || len(f.requests()) != 0 {
		t.Errorf("a cancelled context: err %v, %d requests", err, len(f.requests()))
	}

	f = serve(t, "test-key", reply{status: http.StatusTooManyRequests, header: map[string]string{"Retry-After": "30"}})
	waiting, cancel := context.WithCancel(ctx)
	f.sleep = func(c context.Context, d time.Duration) error { cancel(); return sleep(c, d) } // the real wait, cancelled as it starts
	start := time.Now()
	if _, err := f.Usage(waiting); !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Errorf("cancelled during a Retry-After wait: err %v after %v", err, time.Since(start))
	}
}

func TestNoKey(t *testing.T) {
	t.Setenv("ODDSRELAY_KEY", "")
	f := serve(t, "", jsonReply(fixture(t, "v2-coverage.keyless.json")))
	if _, err := f.Coverage(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got := f.requests()[0].header.Get("Authorization"); got != "" {
		t.Errorf("a client with no key sent Authorization %q", got)
	}
	if f.String() != "oddsrelay.Client(key not set)" {
		t.Errorf("String() = %q", f.String())
	}
}

func TestCacheEviction(t *testing.T) {
	c, err := NewClient("test-key")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= cacheSize; i++ {
		c.remember(fmt.Sprintf("/u%d", i), cached{etag: "e"})
	}
	if _, ok := c.cache["/u0"]; ok || len(c.cache) != cacheSize || len(c.order) != cacheSize {
		t.Errorf("after %d URLs: %d kept, %d in order, first kept %v", cacheSize+1, len(c.cache), len(c.order), ok)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestConcurrentUse(t *testing.T) {
	page := fixture(t, "v2-sports.page.json")
	// A transport with no locks of its own, so -race sees any unguarded use of the client's cache.
	c, err := NewClient("test-key", WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": {`"e1"`}}, Body: io.NopCloser(bytes.NewReader(page))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Sports(ctx, SportsParams{Region: "uk"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
