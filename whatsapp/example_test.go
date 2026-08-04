package whatsapp_test

// This file is compiled as an external consumer of the whatsapp package
// (note the _test package suffix and the absence of any internal import).
// It is therefore a standing proof that the module is usable as a library:
// if anything the public API exposes ever leaks an internal type, or a
// signature drifts, these examples stop compiling.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/piwi3910/whatsapp-go/whatsapp"
)

// Open a client, pair it if needed, and send one message.
func Example() {
	c, err := whatsapp.Open(whatsapp.Options{StateDir: "/var/lib/wa"})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// First run only: pair by scanning the QR code with the phone.
	if !c.IsLoggedIn() {
		qr, err := c.Login(ctx)
		if err != nil {
			log.Fatal(err)
		}
		for evt := range qr {
			if evt.Done {
				break
			}
			fmt.Println("scan this QR:", evt.Code)
		}
	}

	if err := c.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	if _, err := c.SendText(ctx, "+31612345678", "hello from Go"); err != nil {
		log.Fatal(err)
	}
}

// Receive inbound messages in-process. Handlers run on the WhatsApp event
// goroutine, so they must not block.
func ExampleClient_OnEvent() {
	c, err := whatsapp.Open(whatsapp.Options{StateDir: "/var/lib/wa"})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	inbound := make(chan whatsapp.Event, 64)
	c.OnEvent(func(e whatsapp.Event) {
		if e.Type != whatsapp.EventMessageReceived {
			return
		}
		select {
		case inbound <- e: // hand off; never block the event loop
		default: // full: drop rather than stall WhatsApp delivery
		}
	})

	if err := c.Connect(context.Background()); err != nil {
		log.Fatal(err)
	}
	for e := range inbound {
		fmt.Println("inbound:", e.Payload)
	}
}

// Consume the durable event log with a cursor, which is the restart-safe
// way to receive messages: persist the last ID you handled and resume from
// it. Delivery is at-least-once, so deduplicate on the message ID.
func ExampleClient_Events() {
	c, err := whatsapp.Open(whatsapp.Options{StateDir: "/var/lib/wa"})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		log.Fatal(err)
	}

	cursor := loadCursor() // e.g. from your own database
	for {
		events, err := c.Events(ctx, cursor, 100)
		if err != nil {
			log.Print(err)
			time.Sleep(5 * time.Second)
			continue
		}
		for _, e := range events {
			handle(e)
			cursor = e.ID
			saveCursor(cursor)
		}
		if len(events) == 0 {
			time.Sleep(time.Second)
		}
	}
}

// Every operation takes a context, so a slow send can be bounded.
func ExampleClient_SendText() {
	c, err := whatsapp.Open(whatsapp.Options{StateDir: "/var/lib/wa"})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := c.SendText(ctx, "+31612345678", "hello")
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		log.Print("send timed out")
	case err != nil:
		log.Fatal(err)
	default:
		fmt.Println("sent:", resp.MessageID)
	}
}

func loadCursor() int64       { return 0 }
func saveCursor(int64)        {}
func handle(e whatsapp.Event) { _ = e }
