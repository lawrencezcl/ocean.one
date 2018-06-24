package engine

import (
	"context"
	"log"
	"time"
)

const (
	OrderActionCreate = "CREATE"
	OrderActionCancel = "CANCEL"

	EventQueueSize = 8192
)

type TransactCallback func(order *Order, opponent *Order) error
type CancelCallback func(order *Order) error

type OrderEvent struct {
	Order  *Order
	Action string
}

type Book struct {
	PricePrecision  int
	AmountPrecision int
	queue           chan *OrderEvent
	createIndex     map[string]bool
	cancelIndex     map[string]bool
	transact        TransactCallback
	cancel          CancelCallback
	asks            *Page
	bids            *Page
}

func NewBook(pricePrecision, amountPrecision int, transact TransactCallback, cancel CancelCallback) *Book {
	return &Book{
		PricePrecision:  pricePrecision,
		AmountPrecision: amountPrecision,
		queue:           make(chan *OrderEvent, EventQueueSize),
		createIndex:     make(map[string]bool),
		cancelIndex:     make(map[string]bool),
		transact:        transact,
		cancel:          cancel,
		asks:            NewPage(PageSideAsk),
		bids:            NewPage(PageSideBid),
	}
}

func (book *Book) AttachOrderEvent(ctx context.Context, order *Order, action string) error {
	if order.Side != PageSideAsk && order.Side != PageSideBid {
		log.Panicln(order, action)
	}
	if order.Type != OrderTypeLimit && order.Type != OrderTypeMarket {
		log.Panicln(order, action)
	}
	switch action {
	case OrderActionCreate, OrderActionCancel:
		book.queue <- &OrderEvent{Order: order, Action: action}
	default:
		log.Panicln(order, action)
	}
	return nil
}

func (book *Book) process(ctx context.Context, order, opponent *Order) {
	matchedAmount := order.RemainingAmount
	if opponent.RemainingAmount < matchedAmount {
		matchedAmount = opponent.RemainingAmount
	}
	order.RemainingAmount = order.RemainingAmount - matchedAmount
	order.FilledAmount = order.FilledAmount + matchedAmount
	opponent.RemainingAmount = opponent.RemainingAmount - matchedAmount
	opponent.FilledAmount = opponent.FilledAmount + matchedAmount
	for {
		err := book.transact(order, opponent)
		if err == nil {
			break
		}
		log.Println("BOOK ITERATE CALLBACK ERROR", err)
		time.Sleep(100 * time.Millisecond)
	}
}

func (book *Book) createOrder(ctx context.Context, order *Order) {
	if _, found := book.createIndex[order.Id]; found {
		return
	}
	book.createIndex[order.Id] = true

	if order.Side == PageSideAsk {
		book.bids.Iterate(func(opponent *Order) bool {
			if order.Type == OrderTypeLimit && opponent.Price < order.Price {
				return true
			}
			book.process(ctx, order, opponent)
			return order.RemainingAmount == 0
		})
		if order.Type == OrderTypeLimit && order.RemainingAmount > 0 {
			book.asks.Put(order)
		}
	} else if order.Side == PageSideBid {
		book.asks.Iterate(func(opponent *Order) bool {
			if order.Type == OrderTypeLimit && opponent.Price > order.Price {
				return true
			}
			book.process(ctx, order, opponent)
			return order.RemainingAmount == 0
		})
		if order.Type == OrderTypeLimit && order.RemainingAmount > 0 {
			book.bids.Put(order)
		}
	}
}

func (book *Book) cancelOrder(ctx context.Context, order *Order) {
	if _, found := book.cancelIndex[order.Id]; found {
		return
	}
	book.cancelIndex[order.Id] = true

	for {
		err := book.cancel(order)
		if err == nil {
			break
		}
		log.Println("BOOK CANCEL ORDER CALLBACK ERROR", err)
		time.Sleep(100 * time.Millisecond)
	}

	if order.Side == PageSideAsk {
		book.asks.Remove(order)
	} else if order.Side == PageSideBid {
		book.bids.Remove(order)
	} else {
		log.Panicln(order)
	}
}

func (book *Book) Run(ctx context.Context) {
	for {
		select {
		case event := <-book.queue:
			if event.Action == OrderActionCreate {
				book.createOrder(ctx, event.Order)
			} else if event.Action == OrderActionCancel {
				book.cancelOrder(ctx, event.Order)
			} else {
				log.Panicln(event)
			}
		}
	}
}