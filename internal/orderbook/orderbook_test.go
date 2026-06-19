package orderbook

import (
	"errors"
	"testing"
)

// submit is a tiny helper that fails the test on an unexpected error.
func submit(t *testing.T, ob *OrderBook, o Order) []Trade {
	t.Helper()
	trades, err := ob.Submit(o)
	if err != nil {
		t.Fatalf("submit %s: %v", o.ID, err)
	}
	return trades
}

func lim(id string, side Side, price, qty int64) Order {
	return Order{ID: id, Side: side, Type: Limit, Price: price, Quantity: qty}
}

func TestRestsWhenNoMatch(t *testing.T) {
	ob := New()
	trades := submit(t, ob, lim("b1", Buy, 100, 5))
	if len(trades) != 0 {
		t.Fatalf("expected no trades, got %d", len(trades))
	}
	if p, ok := ob.BestBid(); !ok || p != 100 {
		t.Errorf("best bid = %d (%v), want 100", p, ok)
	}
	if _, ok := ob.BestAsk(); ok {
		t.Errorf("ask side should be empty")
	}
}

func TestCrossingProducesTrade(t *testing.T) {
	ob := New()
	submit(t, ob, lim("a1", Sell, 100, 5)) // resting ask
	trades := submit(t, ob, lim("b1", Buy, 100, 5))
	if len(trades) != 1 {
		t.Fatalf("want 1 trade, got %d", len(trades))
	}
	tr := trades[0]
	if tr.TakerID != "b1" || tr.MakerID != "a1" || tr.Price != 100 || tr.Quantity != 5 {
		t.Errorf("bad trade: %+v", tr)
	}
	// Both fully filled -> book empty.
	if _, ok := ob.BestBid(); ok {
		t.Errorf("bid side should be empty")
	}
	if _, ok := ob.BestAsk(); ok {
		t.Errorf("ask side should be empty")
	}
}

func TestPriceTimePriority(t *testing.T) {
	ob := New()
	submit(t, ob, lim("a1", Sell, 100, 3)) // earlier at 100
	submit(t, ob, lim("a2", Sell, 100, 3)) // later at 100
	trades := submit(t, ob, lim("b1", Buy, 100, 4))
	// Should hit a1 fully (3) then a2 partially (1), in that order.
	if len(trades) != 2 {
		t.Fatalf("want 2 trades, got %d: %+v", len(trades), trades)
	}
	if trades[0].MakerID != "a1" || trades[0].Quantity != 3 {
		t.Errorf("first fill should be a1 x3, got %+v", trades[0])
	}
	if trades[1].MakerID != "a2" || trades[1].Quantity != 1 {
		t.Errorf("second fill should be a2 x1, got %+v", trades[1])
	}
	// a2 has 2 left resting.
	asks := ob.Asks()
	if len(asks) != 1 || asks[0].Price != 100 || asks[0].Quantity != 2 {
		t.Errorf("ask depth = %+v, want one level 100 x2", asks)
	}
}

func TestPricePriorityBestFirst(t *testing.T) {
	ob := New()
	submit(t, ob, lim("a1", Sell, 102, 5))
	submit(t, ob, lim("a2", Sell, 100, 5)) // better (lower) ask
	submit(t, ob, lim("a3", Sell, 101, 5))
	// A buy that crosses all should fill cheapest first: 100, then 101, then 102.
	trades := submit(t, ob, lim("b1", Buy, 102, 12))
	if len(trades) != 3 {
		t.Fatalf("want 3 trades, got %d: %+v", len(trades), trades)
	}
	if trades[0].Price != 100 || trades[1].Price != 101 || trades[2].Price != 102 {
		t.Errorf("fills not in price order: %d, %d, %d", trades[0].Price, trades[1].Price, trades[2].Price)
	}
	if trades[0].Quantity != 5 || trades[1].Quantity != 5 || trades[2].Quantity != 2 {
		t.Errorf("quantities wrong: %+v", trades)
	}
}

func TestTakerGetsPriceImprovement(t *testing.T) {
	ob := New()
	submit(t, ob, lim("a1", Sell, 100, 5)) // resting ask at 100
	// Buyer willing to pay up to 105 — but should execute at the maker's 100.
	trades := submit(t, ob, lim("b1", Buy, 105, 5))
	if len(trades) != 1 || trades[0].Price != 100 {
		t.Errorf("taker should fill at maker price 100, got %+v", trades)
	}
}

func TestPartialFillRestsRemainder(t *testing.T) {
	ob := New()
	submit(t, ob, lim("a1", Sell, 100, 3))
	trades := submit(t, ob, lim("b1", Buy, 100, 8)) // 3 fills, 5 should rest as a bid
	if len(trades) != 1 || trades[0].Quantity != 3 {
		t.Fatalf("want one fill x3, got %+v", trades)
	}
	if p, ok := ob.BestBid(); !ok || p != 100 {
		t.Errorf("remainder should rest as bid at 100, got %d (%v)", p, ok)
	}
	if bids := ob.Bids(); len(bids) != 1 || bids[0].Quantity != 5 {
		t.Errorf("bid depth = %+v, want 100 x5", bids)
	}
}

func TestMarketOrderSweepsAndDropsRemainder(t *testing.T) {
	ob := New()
	submit(t, ob, lim("a1", Sell, 100, 2))
	submit(t, ob, lim("a2", Sell, 101, 2))
	// Market buy for 10: takes all 4 available regardless of price, drops the rest.
	trades, err := ob.Submit(Order{ID: "m1", Side: Buy, Type: Market, Quantity: 10})
	if err != nil {
		t.Fatal(err)
	}
	var filled int64
	for _, tr := range trades {
		filled += tr.Quantity
	}
	if filled != 4 {
		t.Errorf("market order filled %d, want 4", filled)
	}
	if _, ok := ob.BestAsk(); ok {
		t.Errorf("asks should be exhausted")
	}
	if _, ok := ob.BestBid(); ok {
		t.Errorf("market remainder must not rest as a bid")
	}
}

func TestCancelRemovesResting(t *testing.T) {
	ob := New()
	submit(t, ob, lim("a1", Sell, 100, 5))
	submit(t, ob, lim("a2", Sell, 100, 5))
	if err := ob.Cancel("a1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// A crossing buy for 5 should now fill against a2, not the cancelled a1.
	trades := submit(t, ob, lim("b1", Buy, 100, 5))
	if len(trades) != 1 || trades[0].MakerID != "a2" {
		t.Errorf("should fill against a2 after a1 cancelled, got %+v", trades)
	}
}

func TestCancelUnknown(t *testing.T) {
	ob := New()
	if err := ob.Cancel("nope"); !errors.Is(err, ErrOrderNotFound) {
		t.Errorf("err = %v, want ErrOrderNotFound", err)
	}
}

func TestCancelFilledOrderFails(t *testing.T) {
	ob := New()
	submit(t, ob, lim("a1", Sell, 100, 5))
	submit(t, ob, lim("b1", Buy, 100, 5)) // fully fills a1
	if err := ob.Cancel("a1"); !errors.Is(err, ErrOrderNotFound) {
		t.Errorf("cancelling a filled order should fail, got %v", err)
	}
}

func TestValidation(t *testing.T) {
	ob := New()
	if _, err := ob.Submit(Order{ID: "x", Side: Buy, Type: Limit, Price: 100, Quantity: 0}); !errors.Is(err, ErrInvalidQuantity) {
		t.Errorf("want ErrInvalidQuantity, got %v", err)
	}
	if _, err := ob.Submit(Order{ID: "x", Side: Buy, Type: Limit, Price: 0, Quantity: 5}); !errors.Is(err, ErrInvalidPrice) {
		t.Errorf("want ErrInvalidPrice, got %v", err)
	}
	submit(t, ob, lim("dup", Buy, 100, 5))
	if _, err := ob.Submit(lim("dup", Buy, 100, 5)); !errors.Is(err, ErrDuplicateID) {
		t.Errorf("want ErrDuplicateID, got %v", err)
	}
}

func TestDepthOrdering(t *testing.T) {
	ob := New()
	submit(t, ob, lim("b1", Buy, 98, 1))
	submit(t, ob, lim("b2", Buy, 100, 1))
	submit(t, ob, lim("b3", Buy, 99, 1))
	bids := ob.Bids()
	if len(bids) != 3 || bids[0].Price != 100 || bids[1].Price != 99 || bids[2].Price != 98 {
		t.Errorf("bids should be high->low: %+v", bids)
	}
	submit(t, ob, lim("a1", Sell, 105, 1))
	submit(t, ob, lim("a2", Sell, 103, 1))
	submit(t, ob, lim("a3", Sell, 104, 1))
	asks := ob.Asks()
	if len(asks) != 3 || asks[0].Price != 103 || asks[1].Price != 104 || asks[2].Price != 105 {
		t.Errorf("asks should be low->high: %+v", asks)
	}
}
