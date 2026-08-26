package networkmanager

import (
	"context"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

// WatchProperties streams NetworkManager PropertiesChanged signals until the context is
// cancelled. The state observer uses it to trigger event-driven reconciliation.
func (c *Client) WatchProperties(ctx context.Context) (<-chan Event, <-chan error, error) {
	matchOptions := []dbus.MatchOption{
		dbus.WithMatchSender(service),
		dbus.WithMatchInterface(propertiesInterface),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchPathNamespace(managerPath),
	}
	if err := c.conn.AddMatchSignalContext(ctx, matchOptions...); err != nil {
		return nil, nil, fmt.Errorf("subscribe to NetworkManager property changes: %w", err)
	}

	signals := make(chan *dbus.Signal, 32)
	events := make(chan Event, 32)
	errors := make(chan error, 1)
	c.conn.Signal(signals)

	go func() {
		defer close(events)
		defer close(errors)
		defer c.conn.RemoveSignal(signals)
		defer func() {
			removeCtx, cancelRemove := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancelRemove()
			if err := c.conn.RemoveMatchSignalContext(removeCtx, matchOptions...); err != nil {
				select {
				case errors <- fmt.Errorf("remove NetworkManager signal subscription: %w", err):
				default:
				}
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case signal, ok := <-signals:
				if !ok {
					return
				}
				event, err := propertyEvent(signal)
				if err != nil {
					select {
					case errors <- err:
					case <-ctx.Done():
					}
					continue
				}
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return events, errors, nil
}

func propertyEvent(signal *dbus.Signal) (Event, error) {
	if signal == nil || len(signal.Body) != 3 {
		return Event{}, errorsForSignal(signal, "expected three PropertiesChanged fields")
	}
	interfaceName, ok := signal.Body[0].(string)
	if !ok {
		return Event{}, errorsForSignal(signal, "invalid interface field")
	}
	changedVariants, ok := signal.Body[1].(map[string]dbus.Variant)
	if !ok {
		return Event{}, errorsForSignal(signal, "invalid changed-properties field")
	}
	invalidated, ok := signal.Body[2].([]string)
	if !ok {
		return Event{}, errorsForSignal(signal, "invalid invalidated-properties field")
	}
	changed := make(map[string]any, len(changedVariants))
	for key, value := range changedVariants {
		changed[key] = normalizeDBusValue(value.Value())
	}
	return Event{
		Path:        string(signal.Path),
		Interface:   interfaceName,
		Changed:     changed,
		Invalidated: invalidated,
	}, nil
}

func normalizeDBusValue(value any) any {
	switch typed := value.(type) {
	case dbus.ObjectPath:
		return string(typed)
	case []dbus.ObjectPath:
		paths := make([]string, len(typed))
		for index, path := range typed {
			paths[index] = string(path)
		}
		return paths
	default:
		return value
	}
}

func errorsForSignal(signal *dbus.Signal, message string) error {
	if signal == nil {
		return fmt.Errorf("decode NetworkManager signal: %s", message)
	}
	return fmt.Errorf("decode NetworkManager signal %s on %s: %s", signal.Name, signal.Path, message)
}
