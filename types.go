package oddsrelay

import "encoding/json"

// The reply types below hold the fields the API description lists. A field the reply leaves out
// or sends as null stays at its zero value, except where a pointer tells null apart from zero;
// Result.Body keeps the whole reply, unknown fields too.

// Quote is a call's price. It is what the Quote methods return.
type Quote struct {
	Cost      int64  `json:"cost"`
	Remaining Amount `json:"remaining"`
	RateCard  string `json:"rate_card"`
}

// Meta is the meta object of a list reply. Fields a route does not send stay zero.
type Meta struct {
	FeedType    string `json:"feed_type"`
	Region      string `json:"region"`
	OddsFormat  string `json:"odds_format"`
	ProcessedAt string `json:"processed_at"`
	Count       int    `json:"count"` // rows in this reply
	Version     string `json:"version"`
	// NextCursor is the Cursor for the next page; "" on the last page and on the matched boards.
	NextCursor string `json:"next_cursor"`
	// Total and BoardVersion come with the raw board: rows on every page, and the board's version.
	Total        int   `json:"total"`
	BoardVersion int64 `json:"board_version"`
	// LastSeen comes with the matched boards: venue key, then sport key, then an ISO 8601 time.
	LastSeen map[string]map[string]string `json:"last_seen"`
}

// MatchedBoard is the reply of Odds.
type MatchedBoard struct {
	Meta Meta           `json:"meta"`
	Data []MatchedEvent `json:"data"`
}

// MatchedEvent is one event on a matched board. Racing events have Venue in place of the two
// teams; EventName names a team fixture that does not split into home and away.
type MatchedEvent struct {
	EventID        string          `json:"event_id"`
	SportKey       string          `json:"sport_key"`
	SportTitle     string          `json:"sport_title"`
	CompetitionKey string          `json:"competition_key"`
	CommenceTime   string          `json:"commence_time"`
	HomeTeam       string          `json:"home_team"`
	AwayTeam       string          `json:"away_team"`
	EventName      string          `json:"event_name"`
	Venue          string          `json:"venue"`
	Markets        []MatchedMarket `json:"markets"`
}

// MatchedMarket is one market of a matched event.
type MatchedMarket struct {
	Key      string           `json:"key"`
	Outcomes []MatchedOutcome `json:"outcomes"`
}

// MatchedOutcome is one selection, with its bookmaker offers and its exchange lay offers.
type MatchedOutcome struct {
	Name  string      `json:"name"`
	Point *float64    `json:"point"` // the line, on markets that have one
	Back  []BackOffer `json:"back"`  // highest price first
	// Lay is a list of LayOffer, lowest price first, except on each-way and extra-place, where it is
	// a PlaceLays. The dutching board sends none. Decode it with json.Unmarshal.
	Lay json.RawMessage `json:"lay"`
}

// BackOffer is a bookmaker's price for a selection.
type BackOffer struct {
	Bookmaker     string  `json:"bookmaker"`
	Price         float64 `json:"price"`
	Link          string  `json:"link"`
	Places        int     `json:"places"`         // each-way and extra-place
	PlaceFraction float64 `json:"place_fraction"` // each-way and extra-place
	DutchID       string  `json:"dutch_id"`       // dutching
}

// LayOffer is an exchange's lay price for a selection.
type LayOffer struct {
	Exchange string  `json:"exchange"`
	Price    float64 `json:"price"`
	// Available is the liquidity at Price; nil when it is unknown, which is not the same as 0.
	Available *float64 `json:"available"`
	Places    int      `json:"places"`
	Link      string   `json:"link"`
}

// PlaceLays is MatchedOutcome.Lay on the each-way and extra-place boards.
type PlaceLays struct {
	Win   []LayOffer `json:"win"`
	Place []LayOffer `json:"place"`
}

// RawPage is the reply of Raw: one page of the raw board.
type RawPage struct {
	Meta Meta       `json:"meta"`
	Data []RawEvent `json:"data"`
}

// RawEvent is one event's prices from every venue that quotes it. It is also the reply of Event.
type RawEvent struct {
	ID             string     `json:"id"`
	ClusterID      string     `json:"cluster_id"`
	SportKey       string     `json:"sport_key"`
	SportTitle     string     `json:"sport_title"`
	CompetitionKey string     `json:"competition_key"`
	PreviousIDs    []string   `json:"previous_ids"`
	CommenceTime   string     `json:"commence_time"`
	HomeTeam       string     `json:"home_team"`
	AwayTeam       string     `json:"away_team"`
	Bookmakers     []RawVenue `json:"bookmakers"`
}

// RawVenue is one bookmaker's or exchange's markets for an event.
type RawVenue struct {
	Key        string      `json:"key"`
	Title      string      `json:"title"`
	Region     string      `json:"region"`
	LastUpdate string      `json:"last_update"`
	Link       string      `json:"link"` // with IncludeLinks
	Markets    []RawMarket `json:"markets"`
}

// RawMarket is one market of a venue.
type RawMarket struct {
	Key        string       `json:"key"`
	LastUpdate string       `json:"last_update"`
	Outcomes   []RawOutcome `json:"outcomes"`
}

// RawOutcome is one selection's price: Price from a bookmaker, or BackPrice, LayPrice and
// Available from a venue that quotes a lay side.
type RawOutcome struct {
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	BackPrice float64 `json:"back_price"`
	LayPrice  float64 `json:"lay_price"`
	// Available is nil when the liquidity is unknown or IncludeLiquidity is false.
	Available *float64 `json:"available"`
}

// SportsPage is the reply of Sports.
type SportsPage struct {
	Meta Meta    `json:"meta"`
	Data []Sport `json:"data"`
}

// Sport is one sport or competition key.
type Sport struct {
	Key            string   `json:"key"`
	Group          string   `json:"group"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Active         bool     `json:"active"`
	HasOutrights   bool     `json:"has_outrights"`
	Regions        []string `json:"regions"`
	Markets        []string `json:"markets"`
	CompetitionKey string   `json:"competition_key"`
}

// EventsPage is the reply of Events.
type EventsPage struct {
	Meta Meta    `json:"meta"`
	Data []Event `json:"data"`
}

// Event is one event of a sport.
type Event struct {
	ID             string   `json:"id"`
	SportKey       string   `json:"sport_key"`
	SportTitle     string   `json:"sport_title"`
	CompetitionKey string   `json:"competition_key"`
	PreviousIDs    []string `json:"previous_ids"`
	CommenceTime   string   `json:"commence_time"`
	HomeTeam       string   `json:"home_team"`
	AwayTeam       string   `json:"away_team"`
	Status         string   `json:"status"`       // "upcoming" or "started"
	HasCoverage    []string `json:"has_coverage"` // the matched boards that carry it
}

// Venues is the reply of Bookmakers.
type Venues struct {
	Meta Meta    `json:"meta"`
	Data []Venue `json:"data"`
}

// Venue is a bookmaker or an exchange.
type Venue struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Regions    []string `json:"regions"`
	IsExchange bool     `json:"is_exchange"`
}

// Regions is the reply of Regions.
type Regions struct {
	Version       string   `json:"version"`
	DefaultRegion string   `json:"default_region"`
	Regions       []Region `json:"regions"`
}

// Region is one region code and what it offers.
type Region struct {
	Code              string   `json:"code"`
	Name              string   `json:"name"`
	Requestable       bool     `json:"requestable"`
	Bookmakers        int      `json:"bookmakers"`
	Exchanges         []string `json:"exchanges"`
	MatchedCoverage   bool     `json:"matched_coverage"`
	CoverageModel     string   `json:"coverage_model"`
	ExchangeAvailable bool     `json:"exchange_available"`
	Board             string   `json:"board"`
}

// Status is the reply of Status: each product's state, "ok", "slow" or "stale".
type Status struct {
	Region      string            `json:"region"`
	GeneratedAt string            `json:"generated_at"`
	Products    map[string]string `json:"products"`
}

// Health is the reply of Health.
type Health struct {
	Status string `json:"status"` // "ok" or "degraded"
	// Version is the API's version path, such as "v2".
	Version       string                 `json:"version"`
	Boards        map[string]BoardHealth `json:"boards"`
	StaleBoards   []string               `json:"stale_boards"`
	MaxBoardAgeS  float64                `json:"max_board_age_s"`
	MissingBoards []string               `json:"missing_boards"`
}

// BoardHealth is one board's entry in Health.Boards. A board that is loaded but not yet stamped
// has ProcessedAt "" and a nil AgeS and Count: read nil as unknown, not as fresh or empty.
type BoardHealth struct {
	ProcessedAt string   `json:"processed_at"`
	AgeS        *float64 `json:"age_s"`
	Count       *int     `json:"count"`
}

// Usage is the reply of Usage.
type Usage struct {
	Plan         string          `json:"plan"`
	RateCard     string          `json:"rate_card"`
	Account      UsageAccount    `json:"account"`
	Tokens       UsageTokens     `json:"tokens"`
	Regions      []string        `json:"regions"`
	Key          UsageKey        `json:"key"`
	FloodCap     FloodCap        `json:"flood_cap"`
	Scope        json.RawMessage `json:"scope"` // null when the account has none
	ScopeVersion int             `json:"scope_version"`
}

// UsageAccount is Usage.Account.
type UsageAccount struct {
	Status           string `json:"status"` // "active" or "suspended"
	SuspensionReason string `json:"suspension_reason"`
	Test             bool   `json:"test"`
}

// UsageTokens is Usage.Tokens: the account's token balance.
type UsageTokens struct {
	Used      Amount `json:"used"`
	Remaining Amount `json:"remaining"`
	Limit     Amount `json:"limit"`
	ResetsAt  string `json:"resets_at"`
}

// UsageKey is Usage.Key: what the key making the call may read.
type UsageKey struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"` // "server", "browser" or "dashboard"
	Products []string `json:"products"`
	Regions  []string `json:"regions"`
}

// FloodCap is Usage.FloodCap: the most requests the key may make in each window.
type FloodCap struct {
	Per10s int `json:"per_10s"`
	PerMin int `json:"per_min"`
}

// PriceTable is the reply of Pricing. Regions holds the per-region tables as sent.
type PriceTable struct {
	RateCard    string          `json:"rate_card"`
	Provisional bool            `json:"provisional"`
	CapturedAt  string          `json:"captured_at"`
	Recent      RecentFactor    `json:"recent"`
	Regions     json.RawMessage `json:"regions"`
}

// RecentFactor is PriceTable.Recent.
type RecentFactor struct {
	MaxAgeS float64 `json:"max_age_s"`
	Factor  float64 `json:"factor"`
}
