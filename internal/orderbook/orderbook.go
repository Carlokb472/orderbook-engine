// Package orderbook implements a limit order book with a price-time priority
// matching engine — the core of any exchange.
//
// Rules:
//   - Orders rest on the book by price, then by arrival time (FIFO) within a
//     price level. The best bid is the highest buy price; the best ask is the
//     lowest sell price.
//   - An incoming order (the "taker") matches against the opposite side until it
//     is filled or no more acceptable price exists. Each fill executes at the
//     resting ("maker") order's price — so the taker may get price improvement.
//   - A limit order rests any unfilled remainder on the book; a market order
//     never rests (unfilled remainder is dropped).
//
// Prices and quantities are integers (minor units / lots) — never floats — so
// arithmetic is exact.
package orderbook

import (
	"container/list"
	"errors"
	"sort"
)

// Side is which way an order trades.
type Side uint8

const (
	Buy Side = iota
	Sell
)

// Type is the order type.
type Type uint8

const (
	Limit Type = iota
	Market
)

var (
	ErrInvalidQuantity = errors.New("orderbook: quantity must be positive")
	ErrInvalidPrice    = errors.New("orderbook: limit price must be positive")
	ErrDuplicateID     = errors.New("orderbook: order id already resting on the book")
	ErrOrderNotFound   = errors.New("orderbook: order not found")
)

// Order is a request to trade. Price is ignored for Market orders.
type Order struct {
	ID       string
	Side     Side
	Type     Type
	Price    int64
	Quantity int64
}

// Trade is one fill produced by matching, priced at the maker's resting price.
type Trade struct {
	TakerID  string `json:"taker_id"`
	MakerID  string `json:"maker_id"`
	Price    int64  `json:"price"`
	Quantity int64  `json:"quantity"`
}

// resting is a live order sitting on the book, plus the bookkeeping needed to
// locate and remove it in O(1) on cancel/fill.
type resting struct {
	order     Order
	remaining int64
	elem      *list.Element
	level     *level
	side      *side
}

// level is all resting orders at one price, in FIFO arrival order.
type level struct {
	price    int64
	orders   *list.List // of *resting
	totalQty int64
}

// side is one half of the book (all bids or all asks). prices is kept sorted
// ascending; the best price is the max (bids) or min (asks).
type side struct {
	isBid  bool
	levels map[int64]*level
	prices []int64
}

func newSide(isBid bool) *side {
	return &side{isBid: isBid, levels: make(map[int64]*level)}
}

func (s *side) best() (int64, bool) {
	if len(s.prices) == 0 {
		return 0, false
	}
	if s.isBid {
		return s.prices[len(s.prices)-1], true
	}
	return s.prices[0], true
}

func (s *side) add(r *resting) {
	lvl, ok := s.levels[r.order.Price]
	if !ok {
		lvl = &level{price: r.order.Price, orders: list.New()}
		s.levels[r.order.Price] = lvl
		i := sort.Search(len(s.prices), func(i int) bool { return s.prices[i] >= r.order.Price })
		s.prices = append(s.prices, 0)
		copy(s.prices[i+1:], s.prices[i:])
		s.prices[i] = r.order.Price
	}
	r.elem = lvl.orders.PushBack(r)
	r.level = lvl
	r.side = s
	lvl.totalQty += r.remaining
}

func (s *side) dropIfEmpty(lvl *level) {
	if lvl.orders.Len() > 0 {
		return
	}
	delete(s.levels, lvl.price)
	i := sort.Search(len(s.prices), func(i int) bool { return s.prices[i] >= lvl.price })
	if i < len(s.prices) && s.prices[i] == lvl.price {
		s.prices = append(s.prices[:i], s.prices[i+1:]...)
	}
}

// OrderBook is a single-instrument order book + matching engine. It is not safe
// for concurrent use; a real venue serialises orders onto one matching thread.
type OrderBook struct {
	bids    *side
	asks    *side
	resting map[string]*resting
}

// New returns an empty order book.
func New() *OrderBook {
	return &OrderBook{
		bids:    newSide(true),
		asks:    newSide(false),
		resting: make(map[string]*resting),
	}
}

// Submit matches an incoming order and rests any unfilled remainder (limit
// orders only). It returns the trades produced, best (taker) order first.
func (ob *OrderBook) Submit(o Order) ([]Trade, error) {
	if o.Quantity <= 0 {
		return nil, ErrInvalidQuantity
	}
	if o.Type == Limit && o.Price <= 0 {
		return nil, ErrInvalidPrice
	}
	if _, exists := ob.resting[o.ID]; exists {
		return nil, ErrDuplicateID
	}

	opp := ob.asks
	if o.Side == Sell {
		opp = ob.bids
	}

	remaining := o.Quantity
	var trades []Trade

	for remaining > 0 {
		best, ok := opp.best()
		if !ok {
			break // opposite side empty
		}
		if o.Type == Limit {
			if o.Side == Buy && best > o.Price {
				break // best ask above our bid — stop
			}
			if o.Side == Sell && best < o.Price {
				break // best bid below our ask — stop
			}
		}

		lvl := opp.levels[best]
		for remaining > 0 && lvl.orders.Len() > 0 {
			front := lvl.orders.Front()
			maker := front.Value.(*resting)

			q := remaining
			if maker.remaining < q {
				q = maker.remaining
			}
			trades = append(trades, Trade{
				TakerID:  o.ID,
				MakerID:  maker.order.ID,
				Price:    best, // execute at the maker's price
				Quantity: q,
			})
			remaining -= q
			maker.remaining -= q
			lvl.totalQty -= q

			if maker.remaining == 0 {
				lvl.orders.Remove(front)
				delete(ob.resting, maker.order.ID)
			}
		}
		opp.dropIfEmpty(lvl)
	}

	if remaining > 0 && o.Type == Limit {
		r := &resting{order: o, remaining: remaining}
		if o.Side == Buy {
			ob.bids.add(r)
		} else {
			ob.asks.add(r)
		}
		ob.resting[o.ID] = r
	}
	return trades, nil
}

// Cancel removes a resting order by id.
func (ob *OrderBook) Cancel(id string) error {
	r, ok := ob.resting[id]
	if !ok {
		return ErrOrderNotFound
	}
	r.level.totalQty -= r.remaining
	r.level.orders.Remove(r.elem)
	r.side.dropIfEmpty(r.level)
	delete(ob.resting, id)
	return nil
}

// BestBid returns the highest resting buy price.
func (ob *OrderBook) BestBid() (int64, bool) { return ob.bids.best() }

// BestAsk returns the lowest resting sell price.
func (ob *OrderBook) BestAsk() (int64, bool) { return ob.asks.best() }

// PriceLevel is an aggregated view of one price in a depth snapshot.
type PriceLevel struct {
	Price    int64 `json:"price"`
	Quantity int64 `json:"quantity"`
	Orders   int   `json:"orders"`
}

// Bids returns aggregated bid levels, best (highest price) first.
func (ob *OrderBook) Bids() []PriceLevel { return ob.bids.snapshot() }

// Asks returns aggregated ask levels, best (lowest price) first.
func (ob *OrderBook) Asks() []PriceLevel { return ob.asks.snapshot() }

func (s *side) snapshot() []PriceLevel {
	out := make([]PriceLevel, 0, len(s.prices))
	appendLevel := func(p int64) {
		lvl := s.levels[p]
		out = append(out, PriceLevel{Price: p, Quantity: lvl.totalQty, Orders: lvl.orders.Len()})
	}
	if s.isBid {
		for i := len(s.prices) - 1; i >= 0; i-- { // highest first
			appendLevel(s.prices[i])
		}
	} else {
		for _, p := range s.prices { // lowest first
			appendLevel(p)
		}
	}
	return out
}
