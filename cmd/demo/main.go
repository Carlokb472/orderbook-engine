// Command demo runs a scripted sequence of orders through the matching engine
// and prints the trades and the resulting book — a zero-setup visual demo.
//
//	go run ./cmd/demo
package main

import (
	"fmt"

	"github.com/Carlokb472/orderbook-engine/internal/orderbook"
)

func main() {
	ob := orderbook.New()

	type step struct {
		desc string
		o    orderbook.Order
	}
	steps := []step{
		{"rest sell 5 @ 101", ord("a1", orderbook.Sell, orderbook.Limit, 101, 5)},
		{"rest sell 5 @ 102", ord("a2", orderbook.Sell, orderbook.Limit, 102, 5)},
		{"rest buy 5 @ 99", ord("b1", orderbook.Buy, orderbook.Limit, 99, 5)},
		{"rest buy 3 @ 100", ord("b2", orderbook.Buy, orderbook.Limit, 100, 3)},
		{"BUY 8 @ 102 (crosses asks)", ord("b3", orderbook.Buy, orderbook.Limit, 102, 8)},
		{"MARKET sell 4 (hits bids)", ord("s1", orderbook.Sell, orderbook.Market, 0, 4)},
	}

	for _, s := range steps {
		trades, err := ob.Submit(s.o)
		fmt.Printf("\n>>> %s\n", s.desc)
		if err != nil {
			fmt.Printf("    error: %v\n", err)
			continue
		}
		if len(trades) == 0 {
			fmt.Println("    (no trades — rested on book)")
		}
		for _, t := range trades {
			fmt.Printf("    TRADE  %s takes %s  qty %d @ %d\n", t.TakerID, t.MakerID, t.Quantity, t.Price)
		}
		printBook(ob)
	}
}

func ord(id string, side orderbook.Side, typ orderbook.Type, price, qty int64) orderbook.Order {
	return orderbook.Order{ID: id, Side: side, Type: typ, Price: price, Quantity: qty}
}

// printBook renders the ladder with asks on top (low to high reversed so the
// spread sits in the middle, like a real book display).
func printBook(ob *orderbook.OrderBook) {
	asks := ob.Asks() // low -> high
	bids := ob.Bids() // high -> low
	fmt.Println("    -------- BOOK --------")
	for i := len(asks) - 1; i >= 0; i-- { // show highest ask on top
		fmt.Printf("    ASK  %6d  x %d\n", asks[i].Price, asks[i].Quantity)
	}
	if len(asks) == 0 {
		fmt.Println("    ASK   (empty)")
	}
	fmt.Println("    - - - - spread - - - -")
	for _, b := range bids {
		fmt.Printf("    BID  %6d  x %d\n", b.Price, b.Quantity)
	}
	if len(bids) == 0 {
		fmt.Println("    BID   (empty)")
	}
	fmt.Println("    ----------------------")
}
