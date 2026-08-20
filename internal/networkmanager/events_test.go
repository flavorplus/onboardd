package networkmanager

import (
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestPropertyEvent(t *testing.T) {
	signal := &dbus.Signal{
		Sender: service,
		Path:   managerPath,
		Name:   propertiesInterface + ".PropertiesChanged",
		Body: []any{
			managerInterface,
			map[string]dbus.Variant{
				"State":             dbus.MakeVariant(uint32(StateConnectedGlobal)),
				"PrimaryConnection": dbus.MakeVariant(dbus.ObjectPath("/active/1")),
			},
			[]string{"Connectivity"},
		},
	}

	event, err := propertyEvent(signal)
	if err != nil {
		t.Fatalf("propertyEvent() error = %v", err)
	}
	if event.Path != string(managerPath) || event.Interface != managerInterface {
		t.Fatalf("event identity = %#v", event)
	}
	if got := event.Changed["PrimaryConnection"]; got != "/active/1" {
		t.Fatalf("PrimaryConnection = %#v", got)
	}
	if !reflect.DeepEqual(event.Invalidated, []string{"Connectivity"}) {
		t.Fatalf("Invalidated = %#v", event.Invalidated)
	}
}

func TestPropertyEventRejectsMalformedSignal(t *testing.T) {
	if _, err := propertyEvent(&dbus.Signal{}); err == nil {
		t.Fatal("expected malformed signal error")
	}
}
