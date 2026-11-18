# oddsrelay-go

The OddsRelay Go client. One method for each data, lookup and historical route of the [OddsRelay API](https://oddsrelay.io/docs), which
serves bookmaker odds for the UK & Ireland and 20 more regions as JSON: raw prices in every region, and UK and Irish
bookmakers matched to exchange lay prices. Standard library only, Go 1.21 and up.

## Install

```sh
go get github.com/oddsrelay/oddsrelay-go
```

## Get a key

Sign up at [app.oddsrelay.io/signup](https://app.oddsrelay.io/signup) and copy your key from the dashboard.
Free gives you 50,000 tokens for 14 days from sign-up, with no card, then no tokens until you choose a plan, on the same key. [Plans and token prices](https://oddsrelay.io/pricing).
Paid plans are for personal use. Any business, even one using the data only in-house, and any product that serves
other people runs on Enterprise; a business can try the data on Free first.

## First call

Put your key in your environment, never in your code:

```sh
export ODDSRELAY_KEY="your key"
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/oddsrelay/oddsrelay-go"
)

func main() {
	client, err := oddsrelay.NewClient("") // reads ODDSRELAY_KEY
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	res, err := client.Odds(ctx, "standard", oddsrelay.OddsParams{
		Region:  "uk",
		Sports:  []string{"soccer"}, // soccer is the API's key for football
		Markets: []string{"h2h"},
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, event := range res.Data.Data {
		fmt.Println(event.CommenceTime, event.HomeTeam, "v", event.AwayTeam)
	}
	fmt.Println("cost:", res.Tokens.Cost, "left:", res.Tokens.Remaining)
}
```

No key yet? These routes answer without one:

```go
status, err := client.Status(ctx, "uk")
coverage, err := client.Coverage(ctx, "uk")
```

## Regions

Raw prices, in-play prices, event states and historical odds come from every region `Regions` lists. `Raw` takes one
code, or several international codes in one call (`uk` is always asked alone). Every other method takes one code.

```go
page, err := client.Raw(ctx, oddsrelay.RawParams{Region: "us", Sports: []string{"basketball"}})
// one board, priced as the two together
both, err := client.Raw(ctx, oddsrelay.RawParams{Region: "us,global", Sports: []string{"basketball"}})
// prices on events in play
live, err := client.Raw(ctx, oddsrelay.RawParams{Region: "us", Sports: []string{"basketball"}, InPlay: "only"})
```

- The matched boards and the exchanges are served for `uk` only: elsewhere `Odds` returns an `*APIError` with status
  403 and `Code` `product_not_in_region`.
- A call with no `Region` reads `uk` when that is your account's home region. With another home region it returns
  an `*APIError` with status 400 and `Code` `region_required`: set `Region`.

## Methods

Every method takes a `context.Context` first.

| Method | Route | What it returns |
|---|---|---|
| `Odds(ctx, board, OddsParams)` | `GET /v2/odds/{type}` | Matched odds for one board: `standard`, `2up`, `dutching`, `each-way`, `extra-place` or `bog` |
| `Raw(ctx, RawParams)` | `GET /v2/odds/raw` | Bookmaker and exchange prices by event, unpaired, a page at a time |
| `RawPages(ctx, RawParams, fn)` | `GET /v2/odds/raw` | Every page of the raw board, one call of `fn` each |
| `Event(ctx, id, EventParams)` | `GET /v2/odds/event/{id}` | One event's raw prices |
| `QuoteOdds`, `QuoteRaw`, `QuoteEvent` | the same three routes, with `quote=true` | What the call would cost, without making it |
| `Sports(ctx, SportsParams)` | `GET /v2/sports` | Sport and competition keys, a page at a time |
| `Events(ctx, sport, EventsParams)` | `GET /v2/events` | Events for one sport key, a page at a time |
| `EventsState(ctx, EventStateParams)` | `GET /v2/events/state` | Each event's state in one region: scheduled, started, gone or merged (no tokens) |
| `EventsChanges(ctx, since, EventStateParams)` | `GET /v2/events/changes` | The state changes after `since`, in order (no tokens) |
| `Bookmakers(ctx)` | `GET /v2/bookmakers` | The venues you can filter on, with their keys |
| `Regions(ctx)` | `GET /v2/regions` | The region codes open to your key |
| `Coverage(ctx, region)` | `GET /v2/coverage` | Which venues and sports each product carries (keyless; more detail with a key) |
| `Status(ctx, region)` | `GET /v2/status` | Whether each product is serving (keyless) |
| `Health(ctx)` | `GET /v2/health` | When each board was last processed, and whether any is behind |
| `Usage(ctx)` | `GET /v2/usage` | Tokens used and left, and what your key may read |
| `Pricing(ctx)` | `GET /v2/pricing` | The token rate card |
| `OpenAPI(ctx)` | `GET /v2/openapi.json` | The API description (keyless) |
| `Healthz(ctx)` | `GET /healthz` | A plain-text liveness check (keyless) |
| `HistoryRaw(ctx, date, sports, HistoryParams)` | `GET /v2/historical/odds/raw` | The raw board as it stood at one moment |
| `HistoryEvent(ctx, id, from, HistoryParams)` | `GET /v2/historical/events/{id}/odds` | One event's price changes over a window of up to 24 hours |
| `Closing(ctx, date, sports, HistoryParams)` | `GET /v2/historical/closing` | Opening and closing prices of the events that started on one UTC day |
| `HistoryFiles(ctx, region)` | `GET /v2/historical/files` | The bulk day files, with each one's size and cost |
| `DownloadFile(ctx, tier, sport, file, region, maxTokens)` | `GET /v2/historical/files/{tier}/{sport}/{file}` | One bulk file, as bytes |

Each method returns a `*Result[T]`. `Data` is the body decoded into a typed struct, and `Body` is the JSON as sent,
with every field kept, including fields the API adds after this release. `Coverage`, `OpenAPI`, `HistoryEvent` and
`Closing` return their JSON as a `json.RawMessage`, since the keyless and keyed coverage replies differ in shape. On a matched board, an
outcome's `Lay` is also left as JSON: a list of `LayOffer`, or on `each-way` and `extra-place` a `PlaceLays`.

## Filters

Each field of a params struct is one query parameter of the [API reference](https://oddsrelay.io/docs/reference),
and a field left at its zero value is not sent. Lists are joined with commas, a `time.Time` is sent in UTC, and the
`*bool` fields take `oddsrelay.Bool(true)` or `oddsrelay.Bool(false)`:

```go
now := time.Now()
res, err := client.Odds(ctx, "each-way", oddsrelay.OddsParams{
	Region:           "uk",
	Sports:           []string{"horse_racing"},
	CommenceTimeFrom: now,
	CommenceTimeTo:   now.Add(6 * time.Hour),
})
```

The client asks for the API's default shapes, the envelope and ISO 8601 dates, so `format` and `dateFormat` are not
among the fields. The API checks every filter itself: an unknown value returns an `*APIError` whose `Valid` lists
the accepted values, where the API sends them.

## Historical odds

```go
us := oddsrelay.HistoryParams{Region: "us", MaxTokens: maxTokens}
board, err := client.HistoryRaw(ctx, "2026-11-17T18:00:00Z", []string{"basketball"}, us)
moves, err := client.HistoryEvent(ctx, eventID, "2026-11-17T12:00:00Z", us) // 24 hours from there, or set To
lines, err := client.Closing(ctx, "2026-11-17", []string{"basketball"}, us)

files, err := client.HistoryFiles(ctx, "uk")
file, err := client.DownloadFile(ctx, "closing", "soccer", "2026-11-17.csv.gz", "uk", maxTokens)
if err == nil {
	err = os.WriteFile("2026-11-17.csv.gz", file.Data, 0o644) // a gzip CSV, saved as sent
}
```

Each region has its own archive with its own first day. A date before it returns an `*APIError` with `Code`
`date_out_of_range`, whose `Earliest` and `Latest` give the range that region holds. Outside `uk` the archive holds
bookmakers only. A file is charged in full when its download starts; `maxTokens` above 0 prices it first, and
`HistoryFiles` lists each file's `Cost`.

## Event states

`EventsState` is a snapshot of every event's state in one region, and `EventsChanges` follows it. Neither spends
tokens.

```go
filters := oddsrelay.EventStateParams{Region: "us", Sports: []string{"basketball"}}
snapshot, err := client.EventsState(ctx, filters)
since := snapshot.Data.Meta.Since // from the first page

log, err := client.EventsChanges(ctx, since, filters) // the same filters as the snapshot
for _, change := range log.Data.Data {
	rows[change.Event.ID] = change.Event
}
since = log.Data.Meta.NextSince // call again at once while log.Data.Meta.More is true
```

If `since` is too old the API answers 410 with `Code` `since_expired`: take a new snapshot.

## Price a call before you make it

`QuoteOdds`, `QuoteRaw` and `QuoteEvent` ask what a call would cost instead of making it. A quote spends no tokens:

```go
quote, err := client.QuoteOdds(ctx, "standard", oddsrelay.OddsParams{Region: "uk"})
if err != nil {
	log.Fatal(err)
}
fmt.Println(quote.Data.Cost, quote.Data.Remaining)
```

Or let the client do it. With `MaxTokens` set, `Odds`, `Raw`, `Event` and the priced history methods quote first and return a
`*QuoteTooHighError`, without making the call, when the cost is above your cap:

```go
res, err := client.Raw(ctx, oddsrelay.RawParams{Region: "uk", Sports: []string{"tennis"}, MaxTokens: limit})
var high *oddsrelay.QuoteTooHighError
if errors.As(err, &high) {
	fmt.Printf("this call costs %d tokens: add filters to narrow it\n", high.Cost)
}
```

## Polling: spend tokens only on changes

The client keeps the `ETag` of each URL it has fetched and sends it back as `If-None-Match`. When nothing has changed
the API answers 304, which spends no tokens, and the result has `NotModified` set, with the `Data` you already have:

```go
for {
	res, err := client.Odds(ctx, "standard", oddsrelay.OddsParams{Region: "uk", Sports: []string{"soccer"}})
	if err != nil {
		log.Fatal(err)
	}
	if !res.NotModified {
		updateMyScreen(res.Data)
	}
	time.Sleep(pollInterval)
}
```

A client keeps the last 32 URLs it has read, so make one client and share it. It is safe for concurrent use.

## Paging

`Raw`, `Sports`, `Events`, `EventsState` and the history methods return a page at a time. `RawPages` walks the raw board for you, to the last page:

```go
err := client.RawPages(ctx, oddsrelay.RawParams{Region: "uk", Sports: []string{"tennis"}},
	func(page *oddsrelay.Result[oddsrelay.RawPage]) error {
		handle(page.Data.Data)
		return nil // return an error to stop
	})
```

For the others, pass each page's `Meta.NextCursor` back as `Cursor`, until it is empty:

```go
params := oddsrelay.SportsParams{Region: "uk"}
for {
	page, err := client.Sports(ctx, params)
	if err != nil {
		log.Fatal(err)
	}
	handle(page.Data.Data)
	if page.Data.Meta.NextCursor == "" {
		break
	}
	params.Cursor = page.Data.Meta.NextCursor
}
```

## Errors

A reply the API refuses returns an `*APIError` with `StatusCode`, `Code`, `Message`, `RequestID` and `Body` (the whole
error, every field kept). A 402 also carries `Cost`, `Remaining` and `ResetsAt`; a 429 or 503 carries `RetryAfter`.
Network failures come back from `net/http` as they are, and a cancelled or expired context returns its own error.

Branch on `Code`, never on the message, which may change. Quote `RequestID` when you contact us.

```go
_, err := client.Odds(ctx, "dutching", oddsrelay.OddsParams{Region: "uk"})
var apiErr *oddsrelay.APIError
if errors.As(err, &apiErr) && apiErr.Code == "insufficient_tokens" {
	fmt.Printf("needs %d tokens, %s left until %s\n", apiErr.Cost, apiErr.Remaining, apiErr.ResetsAt)
}
```

The front door's own 429 and 502 have no body, so their `Code` is empty.

A 429 is retried for you: the client waits the `Retry-After` seconds and tries again, three requests in all. A
`Retry-After` longer than a minute is not waited out: the client returns the error at once and leaves the decision
to you. Every wait ends early if the context is cancelled.

## Tokens and rate limits

Every `Result`, and every `APIError`, carries the reply's headers:

- `Tokens`: `Cost`, `Used`, `Remaining`, `Limit` and `Reset`, from the `X-Tokens-*` headers
- `RateLimit`: `Limit`, `Remaining` and `Reset` (seconds until the window ends), from the `X-RateLimit-*` headers
- `Header`, `ETag` and `RequestID`

The counts are `Amount`s: `Value`, plus `Unlimited` on an unlimited account and `Known`, which is false when the reply
did not carry that header. An `Amount` prints as its number, `unlimited` or `unknown`.

## Your key

The key travels only in the `Authorization` header, and only to routes that read it. The client never puts it in a
URL or an error, and printing a client shows only whether a key is set. Spaces and newlines around the key, common
when it is read from a file, are dropped; a key with any other character a header cannot carry is refused by
`NewClient`, without echoing it. Each request has a default timeout of half a minute: pass
`oddsrelay.WithHTTPClient` with your own `http.Client` to change it.

## Versions

`oddsrelay.ContractVersion` names the version of the API description these methods follow. The API only adds to
`/v2`, so new fields reach you in `Body` without an upgrade, and new error codes arrive in `Code` like any other.

## More from OddsRelay

- [oddsrelay-python](https://github.com/oddsrelay/oddsrelay-python): the Python client, `pip install oddsrelay`
- [oddsrelay-js](https://github.com/oddsrelay/oddsrelay-js): the JavaScript and TypeScript client, `npm install oddsrelay`
- [oddsrelay-r](https://github.com/oddsrelay/oddsrelay-r): the R client
- [oddsrelay-mcp](https://github.com/oddsrelay/oddsrelay-mcp): an MCP server that gives Claude, Cursor and other AI assistants read access, with a token cap
- [oddsrelay-sheets](https://github.com/oddsrelay/oddsrelay-sheets): formulas and a template spreadsheet for Google Sheets
- [examples](https://github.com/oddsrelay/examples): short programs to copy, one folder each
- [Libraries and tools](https://oddsrelay.io/docs/libraries), the [docs](https://oddsrelay.io/docs), the [API reference](https://oddsrelay.io/docs/reference) and the [OpenAPI file](https://api.oddsrelay.io/v2/openapi.json)
- Contact: hello@oddsrelay.io

## Licence

MIT. See [LICENSE](https://github.com/oddsrelay/oddsrelay-go/blob/main/LICENSE).
