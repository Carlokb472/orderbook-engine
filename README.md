# orderbook-engine

A limit **order book** with a **price-time priority matching engine** — the core of any exchange — written in Go with **zero third-party dependencies**.

This is a portfolio project demonstrating market-microstructure fundamentals: how resting orders are organised, how an incoming order matches against them, partial fills, market vs limit orders, and cancels.

## The matching rules

| Rule | Detail |
|---|---|
| **Price priority** | Best bid = highest buy price; best ask = lowest sell price. A taker fills against the best prices first. |
| **Time priority (FIFO)** | Within a price level, the earliest resting order fills first. |
| **Maker price execution** | Each fill executes at the resting (maker) order's price, so a taker can get *price improvement* (e.g. a buy limit at 105 fills against a resting ask at 100 — at 100). |
| **Limit vs market** | A limit order rests any unfilled remainder on the book. A market order sweeps the book at any price and drops what it can't fill (it never rests). |
| **Integer prices/quantities** | Prices and sizes are `int64` (minor units / lots), never floats — exact arithmetic. |

## Design

```
internal/orderbook/   # the engine — no I/O
  orderbook.go        #   Order, Trade, OrderBook, Submit/Cancel, matching
  orderbook_test.go
cmd/demo/             # scripted visual demo (prints trades + the book ladder)
```

Each side of the book is a `map[price]level` plus a sorted slice of prices; the best price is the slice's max (bids) or min (asks). A `level` holds its resting orders in a `container/list` (a FIFO), which gives O(1) removal on fill/cancel. A global `id -> *resting` map locates any order for cancellation in O(1).

**Complexity:** matching touches each price level in best-first order; inserting a new price level is O(log n) to find the slot plus O(n) to shift the slice. That's fine for a clear reference implementation. A production engine replaces the sorted slice with an array of price levels (when the tick range is bounded) or an intrusive balanced tree, and pins the book to a single matching thread — see *Next steps*.

## Run

Requires Go 1.22+.

```bash
go test ./...        # the full matching test suite
go run ./cmd/demo    # watch a scripted scenario match and print the book
```

### What the demo shows

A few orders rest, then a crossing limit buy sweeps the asks (price-then-time), then a market sell hits the bids — printing each trade and the book ladder after every step.

## API sketch

```go
ob := orderbook.New()
ob.Submit(orderbook.Order{ID: "a1", Side: orderbook.Sell, Type: orderbook.Limit, Price: 101, Quantity: 5})
trades, _ := ob.Submit(orderbook.Order{ID: "b1", Side: orderbook.Buy, Type: orderbook.Limit, Price: 101, Quantity: 5})
// trades[0] == {TakerID:"b1", MakerID:"a1", Price:101, Quantity:5}

ob.BestBid(); ob.BestAsk()   // top of book
ob.Bids(); ob.Asks()         // aggregated depth, best first
ob.Cancel("a1")              // remove a resting order
```

## Tested behaviour

- resting when no match; crossing produces a trade
- price priority (cheapest ask / highest bid first) and time priority (FIFO within a level)
- taker price improvement (fills at the maker's price)
- partial fills rest the remainder (limit) / drop it (market)
- market orders sweep multiple levels regardless of price
- cancel removes a resting order so later matches skip it; cancelling unknown/filled orders fails
- validation: positive quantity, positive limit price, duplicate resting id rejected
- depth snapshots ordered best-first

## Next steps

- **Concurrency model**: wrap the book in a single goroutine consuming an order channel (the standard "one matching thread per instrument" design) so it's safe under load without locks.
- **More order types**: immediate-or-cancel (IOC), fill-or-kill (FOK), post-only, stop orders.
- **Performance**: replace the sorted price slice with an array indexed by tick (bounded price range) or an intrusive red-black tree; benchmark with `testing.B`.
- **A market-data feed**: emit book deltas / trades over a channel or websocket (this is the natural bridge to a feed-handler project).
```
