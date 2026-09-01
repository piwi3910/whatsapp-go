package whatsapp

import (
	"errors"
	"testing"

	"go.mau.fi/whatsmeow"
)

func TestMapQRItem(t *testing.T) {
	got, ok := mapQRItem(whatsmeow.QRChannelItem{Event: "code", Code: "abc"})
	if !ok || got.Code != "abc" || got.Done || got.Err != nil {
		t.Errorf("code item = (%+v, %v), want (code=abc, ok)", got, ok)
	}

	got, ok = mapQRItem(whatsmeow.QRChannelItem{Event: "success"})
	if !ok || !got.Done || got.Err != nil {
		t.Errorf("success item = (%+v, %v), want done without error", got, ok)
	}

	err := errors.New("device limit reached")
	got, ok = mapQRItem(whatsmeow.QRChannelItem{Event: "error", Error: err})
	if !ok || !got.Done || got.Err != err {
		t.Errorf("error item = (%+v, %v), want done with the pairing error", got, ok)
	}

	// A nil Error must still produce a non-nil Err so callers can tell an
	// error terminal from a clean success.
	got, ok = mapQRItem(whatsmeow.QRChannelItem{Event: "error"})
	if !ok || !got.Done || got.Err == nil {
		t.Errorf("error item without Error = (%+v, %v), want done with a fallback error", got, ok)
	}

	if _, ok := mapQRItem(whatsmeow.QRChannelItem{Event: "event"}); ok {
		t.Error("unknown event must not be surfaced (ok=false)")
	}
}
