package oddsrelay_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/oddsrelay/oddsrelay-go"
)

func Example() {
	// A stand-in for the API, so this example runs without a network or a key.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"region":"uk","generated_at":"2026-09-17T02:20:30Z","products":{"standard":"ok","raw":"ok"}}`)
	}))
	defer api.Close()

	// With no WithBaseURL, the client calls https://api.oddsrelay.io.
	client, err := oddsrelay.NewClient("", oddsrelay.WithBaseURL(api.URL))
	if err != nil {
		log.Fatal(err)
	}
	status, err := client.Status(context.Background(), "uk")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(status.Data.Region, "standard:", status.Data.Products["standard"])
	// Output: uk standard: ok
}

func ExampleClient_RawPages() {
	client, err := oddsrelay.NewClient("") // reads ODDSRELAY_KEY
	if err != nil {
		log.Fatal(err)
	}
	err = client.RawPages(context.Background(), oddsrelay.RawParams{Region: "uk", Sports: []string{"tennis"}},
		func(page *oddsrelay.Result[oddsrelay.RawPage]) error {
			for _, event := range page.Data.Data {
				fmt.Println(event.CommenceTime, event.HomeTeam, "v", event.AwayTeam)
			}
			return nil
		})
	if err != nil {
		log.Fatal(err)
	}
}

func ExampleClient_Odds_maxTokens() {
	client, err := oddsrelay.NewClient("")
	if err != nil {
		log.Fatal(err)
	}
	board, err := client.Odds(context.Background(), "standard", oddsrelay.OddsParams{
		Region:    "uk",
		Sports:    []string{"soccer"},
		MaxTokens: 50, // quote first, and make the call only if it costs 50 tokens or fewer
	})
	var high *oddsrelay.QuoteTooHighError
	if errors.As(err, &high) {
		fmt.Println("this call costs", high.Cost, "tokens: add filters to narrow it")
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(board.Data.Data), "events, cost", board.Tokens.Cost)
}
