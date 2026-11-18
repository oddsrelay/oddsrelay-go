# Changelog

## Unreleased

- `EventsState` and `EventsChanges`: each event's state in one region, and the changes after a position.
- Historical odds: `HistoryRaw`, `HistoryEvent`, `Closing`, `HistoryFiles` and `DownloadFile`, which returns the
  file's bytes. The priced ones quote first when `MaxTokens` is set.
- Regions: `RawParams.Region` takes one region code or several international codes joined with commas
  (`"us,global"`), and `Meta.Regions` holds a list reply's codes. `InPlay` on `OddsParams`, `RawParams` and
  `EventParams`.
- `APIError` carries `Earliest` and `Latest` on a 400 `date_out_of_range`.
- README: the regions, the `region_required`, `product_not_in_region` and `date_out_of_range` error codes, and
  in-play prices.

## 0.1.0 (2026-10-19)

The first version, following API description version 2026-07-08.

- One method for each GET route of the OddsRelay API, each taking a `context.Context`, with typed params and replies.
- The key comes from `NewClient` or `ODDSRELAY_KEY` and travels only in the `Authorization` header.
- Every request asks for gzip and the reply is decompressed.
- `QuoteOdds`, `QuoteRaw` and `QuoteEvent` price a call; `MaxTokens` quotes first and returns a `*QuoteTooHighError`
  above your cap.
- `RawPages` follows the raw board's cursor to the last page.
- Conditional requests: each URL's `ETag` is sent back as `If-None-Match`, and a 304 returns the last reply with
  `NotModified` set.
- A 429 waits `Retry-After`, up to a minute, and is retried, three requests in all.
- Errors are `*APIError` with `Code` and `RequestID`; a 402 carries `Cost`, `Remaining` and `ResetsAt`.
- Token and rate-limit headers on every result and error.
